package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"polyglot-meet/ai"
	"polyglot-meet/room"
)

const VoiceCloneEnabled = false

// Hold short STT fragments briefly so we translate full phrases, not "I don't" / "it."
const (
	fragmentHold     = 850 * time.Millisecond
	maxBufferedRunes = 220
)

type AudioChunk struct {
	SpeakerID  string
	SourceLang string
	MIMEType   string
	Data       []byte
}

type speakerBuf struct {
	mu   sync.Mutex
	text string
	lang string
	timer *time.Timer
}

type Pipeline struct {
	room        *room.Room
	ai          ai.AIProvider
	chunkC      chan AudioChunk
	done        chan struct{}
	once        sync.Once
	voiceClones *ai.VoiceCloneRegistry
	bufs        sync.Map // speakerID -> *speakerBuf
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
	if isNonSpeech(transcript) || isPromptLeak(transcript) {
		log.Printf("[pipeline] STT filtered (silence/hallucination) speaker=%s text=%q", speaker.Name, transcript)
		p.broadcastTranslating(chunk.SpeakerID, speaker.Name, false)
		return
	}
	log.Printf("[pipeline] STT speaker=%s latency=%s text=%q", speaker.Name, time.Since(start), trim(transcript, 60))

	readyText, readyLang := p.bufferTranscript(chunk.SpeakerID, chunk.SourceLang, transcript)
	if readyText == "" {
		// Holding a short fragment — flush timer will deliver shortly.
		return
	}

	p.deliver(ctx, chunk.SpeakerID, speaker, readyLang, readyText, start)
}

// bufferTranscript merges short incomplete STT pieces. Returns text+lang when ready to translate.
func (p *Pipeline) bufferTranscript(speakerID, lang, text string) (string, string) {
	v, _ := p.bufs.LoadOrStore(speakerID, &speakerBuf{})
	b := v.(*speakerBuf)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}

	// If language changed, flush whatever we were holding first.
	if b.text != "" && b.lang != "" && b.lang != lang {
		prevText, prevLang := b.text, b.lang
		b.text = strings.TrimSpace(text)
		b.lang = lang
		sid := speakerID
		go p.flushNow(sid, prevLang, prevText)
		if looksComplete(b.text) || utf8.RuneCountInString(b.text) >= maxBufferedRunes {
			out := b.text
			outLang := b.lang
			b.text = ""
			return out, outLang
		}
		b.timer = time.AfterFunc(fragmentHold, func() { p.flushSpeaker(speakerID) })
		return "", ""
	}

	joined := strings.TrimSpace(text)
	if b.text != "" {
		joined = strings.TrimSpace(b.text + " " + text)
	}
	b.text = joined
	b.lang = lang

	if looksComplete(joined) || utf8.RuneCountInString(joined) >= maxBufferedRunes {
		out := b.text
		outLang := b.lang
		b.text = ""
		return out, outLang
	}

	b.timer = time.AfterFunc(fragmentHold, func() { p.flushSpeaker(speakerID) })
	return "", ""
}

func (p *Pipeline) flushNow(speakerID, lang, text string) {
	text = strings.TrimSpace(text)
	if text == "" || isNonSpeech(text) {
		return
	}
	speaker, ok := p.room.Get(speakerID)
	if !ok {
		return
	}
	p.deliver(context.Background(), speakerID, speaker, lang, text, time.Now())
}

func (p *Pipeline) flushSpeaker(speakerID string) {
	v, ok := p.bufs.Load(speakerID)
	if !ok {
		return
	}
	b := v.(*speakerBuf)
	b.mu.Lock()
	text := strings.TrimSpace(b.text)
	lang := b.lang
	b.text = ""
	b.timer = nil
	b.mu.Unlock()

	if text == "" || isNonSpeech(text) {
		if speaker, ok := p.room.Get(speakerID); ok {
			p.broadcastTranslating(speakerID, speaker.Name, false)
		}
		return
	}
	speaker, ok := p.room.Get(speakerID)
	if !ok {
		return
	}
	p.deliver(context.Background(), speakerID, speaker, lang, text, time.Now())
}

func (p *Pipeline) deliver(ctx context.Context, speakerID string, speaker *room.Participant, sourceLang, transcript string, start time.Time) {
	listeners := p.room.Others(speakerID)
	if len(listeners) == 0 {
		p.broadcastTranslating(speakerID, speaker.Name, false)
		return
	}

	var wg sync.WaitGroup
	for _, listener := range listeners {
		wg.Add(1)
		go func(l *room.Participant) {
			defer wg.Done()
			targetLang, _ := l.GetLangs()

			var translated string
			if targetLang == sourceLang {
				translated = transcript
			} else {
				var terr error
				translated, terr = p.ai.Translate(ctx, transcript, sourceLang, targetLang)
				if terr != nil {
					log.Printf("[pipeline] translate error: %v", terr)
					translated = transcript
				}
			}
			if strings.TrimSpace(translated) == "" {
				return
			}
			log.Printf("[pipeline] caption %s→%s lang=%s→%s total=%s text=%q",
				speaker.Name, l.Name, sourceLang, targetLang, time.Since(start), trim(translated, 40))

			p.sendCaption(l, speaker, translated, sourceLang, true)
		}(listener)
	}
	wg.Wait()
	p.broadcastTranslating(speakerID, speaker.Name, false)
}

func looksComplete(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	words := strings.Fields(t)
	if len(words) >= 6 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(t)
	return r == '.' || r == '!' || r == '?' || r == '。' || r == '؟' || r == '！'
}

func isPromptLeak(t string) bool {
	lower := strings.ToLower(t)
	for _, p := range []string{
		"ignore background", "transcribe only", "live conversation",
		"background noise", "background voice", "clear speech",
	} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func (p *Pipeline) sendCaption(listener, speaker *room.Participant, text, sourceLang string, isFinal bool) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type":        "caption",
		"speakerId":   speaker.ID,
		"speakerName": speaker.Name,
		"text":        text,
		"sourceLang":  sourceLang,
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
		"thank you for watching", "thanks",
		"bye", "goodbye", "see you", "see you next time",
		"you", "hmm", "um", "uh", "ah",
		"[silence]", "[noise]", "[music]", "[applause]",
		"...", ". . .", ".", ",", "?", "!",
		"subtitles by", "captions by", "subscribe",
		"the end", "null",
		"ignore background noise", "ignore background voice",
		"transcribe only clear speech", "live conversation spoken",
	}
	lower := strings.ToLower(strings.TrimSpace(strings.Trim(t, ".,!? ")))
	for _, h := range hallucinations {
		if lower == h {
			return true
		}
	}
	runes := []rune(lower)
	if len(runes) <= 1 {
		return true
	}
	for _, r := range runes {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
