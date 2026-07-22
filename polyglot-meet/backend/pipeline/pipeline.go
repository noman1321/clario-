package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"polyglot-meet/ai"
	"polyglot-meet/room"
)

const VoiceCloneEnabled = false

type AudioChunk struct {
	SpeakerID  string
	SourceLang string
	MIMEType   string
	Data       []byte
}

type Pipeline struct {
	room        *room.Room
	ai          ai.AIProvider
	chunkC      chan AudioChunk
	done        chan struct{}
	once        sync.Once
	voiceClones *ai.VoiceCloneRegistry
}

func New(r *room.Room, provider ai.AIProvider, apiKey, ttsModel string) *Pipeline {
	return &Pipeline{
		room:        r,
		ai:          provider,
		chunkC:      make(chan AudioChunk, 64),
		done:        make(chan struct{}),
		voiceClones: ai.NewVoiceCloneRegistry(apiKey, ttsModel),
	}
}

func (p *Pipeline) Push(chunk AudioChunk) {
	select {
	case p.chunkC <- chunk:
	default:
		log.Printf("[pipeline] room=%s chunk dropped", p.room.ID)
	}
}

func (p *Pipeline) RegisterVoiceProfile(id string, data []byte, mimeType string) {
	p.voiceClones.Register(id, data, mimeType)
}

func (p *Pipeline) RemoveVoiceProfile(id string) {
	p.voiceClones.Remove(id)
}

func (p *Pipeline) Start(ctx context.Context) {
	p.once.Do(func() { go p.run(ctx) })
}

func (p *Pipeline) Stop() {
	close(p.done)
}

func (p *Pipeline) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case chunk := <-p.chunkC:
			go p.process(ctx, chunk)
		}
	}
}

func (p *Pipeline) process(ctx context.Context, chunk AudioChunk) {
	if len(chunk.Data) == 0 {
		return
	}
	speaker, ok := p.room.Get(chunk.SpeakerID)
	if !ok {
		return
	}

	start := time.Now()
	p.broadcastTranslating(chunk.SpeakerID, speaker.Name, true)

	transcript, err := p.ai.Transcribe(ctx, chunk.Data, chunk.MIMEType, chunk.SourceLang)
	if err != nil {
		log.Printf("[pipeline] STT error: %v", err)
		p.broadcastTranslating(chunk.SpeakerID, speaker.Name, false)
		return
	}
	if isNonSpeech(transcript) {
		log.Printf("[pipeline] STT filtered (silence/hallucination) speaker=%s text=%q", speaker.Name, transcript)
		p.broadcastTranslating(chunk.SpeakerID, speaker.Name, false)
		return
	}
	log.Printf("[pipeline] STT speaker=%s latency=%s text=%q", speaker.Name, time.Since(start), trim(transcript, 60))

	listeners := p.room.Others(chunk.SpeakerID)
	if len(listeners) == 0 {
		p.broadcastTranslating(chunk.SpeakerID, speaker.Name, false)
		return
	}

	var wg sync.WaitGroup
	for _, listener := range listeners {
		wg.Add(1)
		go func(l *room.Participant) {
			defer wg.Done()
			targetLang, _ := l.GetLangs()

			var translated string
			if targetLang == chunk.SourceLang {
				translated = transcript
			} else {
				var terr error
				translated, terr = p.ai.Translate(ctx, transcript, chunk.SourceLang, targetLang)
				if terr != nil {
					log.Printf("[pipeline] translate error: %v", terr)
					translated = transcript
				}
			}
			log.Printf("[pipeline] caption %s→%s lang=%s→%s total=%s text=%q",
				speaker.Name, l.Name, chunk.SourceLang, targetLang, time.Since(start), trim(translated, 40))

			p.sendCaption(l, speaker, translated, true)
			// TTS handled client-side via Web Speech API — no server synthesis needed
		}(listener)
	}
	wg.Wait()
	p.broadcastTranslating(chunk.SpeakerID, speaker.Name, false)
}

func (p *Pipeline) sendCaption(listener, speaker *room.Participant, text string, isFinal bool) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type":        "caption",
		"speakerId":   speaker.ID,
		"speakerName": speaker.Name,
		"text":        text,
		"isFinal":     isFinal,
	})
	select {
	case listener.Send <- msg:
	default:
	}
}

func (p *Pipeline) sendTTS(ctx context.Context, listener, speaker *room.Participant, text, lang string, start time.Time) {
	var audioBytes []byte
	var mimeType string
	var err error

	if VoiceCloneEnabled && p.voiceClones.HasProfile(speaker.ID) {
		audioBytes, mimeType, err = p.voiceClones.SynthesizeCloned(ctx, speaker.ID, text, lang, p.ai.Synthesize)
	} else {
		audioBytes, mimeType, err = p.ai.Synthesize(ctx, text, lang, "")
	}
	if err != nil || len(audioBytes) == 0 {
		return
	}
	log.Printf("[pipeline] TTS listener=%s total=%s", listener.Name, time.Since(start))

	msg, _ := json.Marshal(map[string]interface{}{
		"type":      "audio_dub",
		"speakerId": speaker.ID,
		"mimeType":  mimeType,
		"data":      base64.StdEncoding.EncodeToString(audioBytes),
	})
	select {
	case listener.Send <- msg:
	default:
	}
}

func (p *Pipeline) broadcastTranslating(speakerID, speakerName string, active bool) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type":        "translating",
		"speakerId":   speakerID,
		"speakerName": speakerName,
		"active":      active,
	})
	for _, l := range p.room.Others(speakerID) {
		select {
		case l.Send <- msg:
		default:
		}
	}
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// isNonSpeech filters empty strings and known Whisper hallucinations on silence/noise.
func isNonSpeech(t string) bool {
	if strings.TrimSpace(t) == "" {
		return true
	}
	hallucinations := []string{
		"thank you", "thanks for watching", "thanks for listening",
		"bye", "goodbye", "see you", "see you next time",
		"you", "hmm", "um", "uh", "ah",
		"[silence]", "[noise]", "[music]", "[applause]",
		"...", ". . .", ".", ",",
		"subtitles by", "captions by",
	}
	lower := strings.ToLower(strings.TrimSpace(strings.Trim(t, ".,!? ")))
	for _, h := range hallucinations {
		if lower == h {
			return true
		}
	}
	// skip single characters
	if len([]rune(lower)) <= 1 {
		return true
	}
	return false
}
