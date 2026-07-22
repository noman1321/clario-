package ai

import "context"

// HybridProvider routes STT+Translation to Groq and TTS to Gemini.
type HybridProvider struct {
	stt   *GroqProvider
	tts   *GeminiProvider
}

func NewHybridProvider(groq *GroqProvider, gemini *GeminiProvider) *HybridProvider {
	return &HybridProvider{stt: groq, tts: gemini}
}

func (h *HybridProvider) Transcribe(ctx context.Context, audioData []byte, mimeType, lang string) (string, error) {
	return h.stt.Transcribe(ctx, audioData, mimeType, lang)
}

func (h *HybridProvider) Translate(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	return h.stt.Translate(ctx, text, sourceLang, targetLang)
}

func (h *HybridProvider) Synthesize(ctx context.Context, text, lang, voiceID string) ([]byte, string, error) {
	return h.tts.Synthesize(ctx, text, lang, voiceID)
}
