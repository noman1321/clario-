package ai

import "context"

// HybridProvider: Groq Whisper for STT, Gemini Flash for translation, Gemini for TTS.
type HybridProvider struct {
	stt    *GroqProvider
	gemini *GeminiProvider
}

func NewHybridProvider(groq *GroqProvider, gemini *GeminiProvider) *HybridProvider {
	return &HybridProvider{stt: groq, gemini: gemini}
}

func (h *HybridProvider) Transcribe(ctx context.Context, audioData []byte, mimeType, lang string) (string, error) {
	return h.stt.Transcribe(ctx, audioData, mimeType, lang)
}

func (h *HybridProvider) Translate(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	out, err := h.gemini.Translate(ctx, text, sourceLang, targetLang)
	if err == nil && out != "" {
		return out, nil
	}
	return h.stt.Translate(ctx, text, sourceLang, targetLang)
}

func (h *HybridProvider) Synthesize(ctx context.Context, text, lang, voiceID string) ([]byte, string, error) {
	return h.gemini.Synthesize(ctx, text, lang, voiceID)
}
