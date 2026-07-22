package ai

import "context"

type AIProvider interface {
	Transcribe(ctx context.Context, audioData []byte, mimeType, lang string) (string, error)
	Translate(ctx context.Context, text, sourceLang, targetLang string) (string, error)
	Synthesize(ctx context.Context, text, lang, voiceID string) ([]byte, string, error)
}

func LangName(code string) string {
	names := map[string]string{
		"en": "English", "es": "Spanish", "hi": "Hindi",
		"fr": "French", "zh": "Mandarin Chinese", "de": "German",
		"ar": "Arabic", "pt": "Portuguese", "ja": "Japanese",
		"ko": "Korean", "ru": "Russian", "it": "Italian",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return code
}
