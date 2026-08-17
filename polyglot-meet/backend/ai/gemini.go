package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type GeminiProvider struct {
	client     *genai.Client
	apiKey     string
	sttModel   string
	transModel string
	ttsModel   string
	httpClient *http.Client
}

func NewGeminiProvider(apiKey, sttModel, transModel, ttsModel string) (*GeminiProvider, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &GeminiProvider{
		client:     client,
		apiKey:     apiKey,
		sttModel:   sttModel,
		transModel: resolveGeminiTransModel(transModel),
		ttsModel:   ttsModel,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func resolveGeminiTransModel(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(n, "gemini") && !strings.Contains(n, "tts") {
		return name
	}
	return "gemini-2.5-flash"
}

func (g *GeminiProvider) Transcribe(ctx context.Context, audioData []byte, mimeType, lang string) (string, error) {
	if len(audioData) == 0 {
		return "", nil
	}
	model := g.client.GenerativeModel(g.sttModel)
	temp := float32(0)
	model.Temperature = &temp

	prompt := fmt.Sprintf(`You are a strict speech-to-text engine. Transcribe ONLY words that were actually spoken in this audio clip.

Language spoken: %s

Hard rules — violating any rule is wrong:
1. Return ONLY the spoken words, verbatim. Nothing else.
2. Do NOT guess, hallucinate, infer, or complete partial words.
3. Do NOT add punctuation marks, formatting, or explanations.
4. If the audio contains silence, breathing, background noise, music, or unclear sounds → return exactly: [silence]
5. If you are not confident that real speech occurred → return: [silence]

Reply with the transcribed text OR [silence]. No other output.`, LangName(lang))

	resp, err := model.GenerateContent(ctx,
		genai.Text(prompt),
		genai.Blob{MIMEType: mimeType, Data: audioData},
	)
	if err != nil {
		return "", fmt.Errorf("gemini STT: %w", err)
	}
	text := cleanTranscript(extractText(resp))
	return text, nil
}

// cleanTranscript normalises Gemini STT output and rejects known non-speech patterns.
func cleanTranscript(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	lower := strings.ToLower(t)
	noSpeech := []string{
		"[silence]", "[noise]", "[inaudible]", "[no speech]",
		"[background noise]", "[music]", "[laughter]", "[applause]",
		"[breathing]", "[cough]", "[static]",
		"no speech", "silence", "inaudible", "no audio",
	}
	for _, pat := range noSpeech {
		if lower == pat || strings.HasPrefix(lower, pat) {
			return ""
		}
	}
	// Strip markdown/formatting Gemini sometimes adds
	t = strings.Trim(t, "`*_\"'")
	return t
}

func (g *GeminiProvider) Translate(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || sourceLang == targetLang {
		return text, nil
	}
	model := g.client.GenerativeModel(g.transModel)
	temp := float32(0)
	model.Temperature = &temp
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(interpreterSystem(sourceLang, targetLang))},
	}

	resp, err := model.GenerateContent(ctx, genai.Text(text))
	if err != nil {
		return "", fmt.Errorf("gemini translate: %w", err)
	}
	result := cleanTranslation(extractText(resp))
	if result == "" {
		return text, nil
	}
	return result, nil
}

// TTS REST types
type ttsReq struct {
	Contents         []ttsContent  `json:"contents"`
	GenerationConfig ttsGenCfg     `json:"generationConfig"`
}
type ttsContent struct{ Parts []ttsPart `json:"parts"` }
type ttsPart struct{ Text string `json:"text"` }
type ttsGenCfg struct {
	ResponseModalities []string    `json:"responseModalities"`
	SpeechConfig       speechCfg   `json:"speechConfig"`
}
type speechCfg struct{ VoiceConfig voiceCfg `json:"voiceConfig"` }
type voiceCfg struct{ PrebuiltVoiceConfig prebuiltVoice `json:"prebuiltVoiceConfig"` }
type prebuiltVoice struct{ VoiceName string `json:"voiceName"` }
type ttsResp struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func (g *GeminiProvider) Synthesize(ctx context.Context, text, lang, voiceID string) ([]byte, string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, "", nil
	}
	voice := voiceID
	if voice == "" {
		voice = "Aoede"
	}

	payload := ttsReq{
		Contents: []ttsContent{{Parts: []ttsPart{{Text: text}}}},
		GenerationConfig: ttsGenCfg{
			ResponseModalities: []string{"AUDIO"},
			SpeechConfig: speechCfg{VoiceConfig: voiceCfg{
				PrebuiltVoiceConfig: prebuiltVoice{VoiceName: voice},
			}},
		},
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		g.ttsModel, g.apiKey,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Printf("[TTS] error %d: %s", resp.StatusCode, string(raw))
		return nil, "", fmt.Errorf("tts api error: %d", resp.StatusCode)
	}

	var r ttsResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, "", err
	}
	for _, c := range r.Candidates {
		for _, p := range c.Content.Parts {
			if p.InlineData != nil && p.InlineData.Data != "" {
				b, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
				if err != nil {
					return nil, "", err
				}
				mimeStr := p.InlineData.MIMEType
				// Gemini TTS returns raw L16 PCM — wrap in WAV so browsers can decode it
				if strings.Contains(mimeStr, "L16") || strings.Contains(mimeStr, "pcm") {
					rate := parseSampleRate(mimeStr, 24000)
					b = pcmToWAV(b, rate)
					mimeStr = "audio/wav"
				}
				return b, mimeStr, nil
			}
		}
	}
	return nil, "", fmt.Errorf("tts: no audio in response")
}

// pcmToWAV wraps raw L16 PCM bytes in a valid WAV container so browsers can decode it.
func pcmToWAV(pcm []byte, sampleRate uint32) []byte {
	numCh := uint16(1)
	bps := uint16(16)
	byteRate := sampleRate * uint32(numCh) * uint32(bps) / 8
	blockAlign := numCh * bps / 8
	dataLen := uint32(len(pcm))
	buf := make([]byte, 44+len(pcm))
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], 36+dataLen)
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:], numCh)
	binary.LittleEndian.PutUint32(buf[24:], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:], byteRate)
	binary.LittleEndian.PutUint16(buf[32:], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:], bps)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], dataLen)
	copy(buf[44:], pcm)
	return buf
}

// parseSampleRate extracts "rate=XXXXX" from a MIME type string.
func parseSampleRate(mimeType string, defaultRate uint32) uint32 {
	for _, part := range strings.Split(mimeType, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "rate=") {
			if r, err := strconv.ParseUint(strings.TrimPrefix(part, "rate="), 10, 32); err == nil {
				return uint32(r)
			}
		}
	}
	return defaultRate
}

func extractText(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	c := resp.Candidates[0]
	if c.Content == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range c.Content.Parts {
		if t, ok := p.(genai.Text); ok {
			sb.WriteString(string(t))
		}
	}
	return sb.String()
}
