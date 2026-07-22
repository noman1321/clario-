package config

import (
	"log"
	"os"
)

type Config struct {
	Addr         string
	GeminiAPIKey string
	GroqAPIKey   string
	TURNHost     string
	TURNPort     string
	TURNUser     string
	TURNPass     string
	STTModel     string
	TransModel   string
	TTSModel     string
	FrontendPath string
}

func Load() *Config {
	return &Config{
		Addr:         getEnv("ADDR", ":8080"),
		GeminiAPIKey: mustEnv("GEMINI_API_KEY"),
		GroqAPIKey:   getEnv("GROQ_API_KEY", ""),
		TURNHost:     getEnv("TURN_HOST", ""),
		TURNPort:     getEnv("TURN_PORT", "3478"),
		TURNUser:     getEnv("TURN_USER", "polyglot"),
		TURNPass:     getEnv("TURN_PASS", "polyglot123"),
		STTModel:     getEnv("STT_MODEL", "whisper-large-v3-turbo"),
		TransModel:   getEnv("TRANS_MODEL", "llama-3.3-70b-versatile"),
		TTSModel:     getEnv("TTS_MODEL", "gemini-2.5-flash-preview-tts"),
		FrontendPath: getEnv("FRONTEND_PATH", "../frontend"),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}
