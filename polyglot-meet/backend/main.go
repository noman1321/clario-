package main

import (
	"log"
	"net/http"

	"polyglot-meet/ai"
	"polyglot-meet/config"
	"polyglot-meet/room"
	"polyglot-meet/signaling"
)

func main() {
	cfg := config.Load()

	gemini, err := ai.NewGeminiProvider(cfg.GeminiAPIKey, cfg.STTModel, cfg.TransModel, cfg.TTSModel)
	if err != nil {
		log.Fatalf("gemini provider: %v", err)
	}

	var provider ai.AIProvider
	if cfg.GroqAPIKey != "" {
		groq := ai.NewGroqProvider(cfg.GroqAPIKey, cfg.STTModel, cfg.TransModel)
		provider = ai.NewHybridProvider(groq, gemini)
		log.Printf("[ai] using Groq Whisper for STT, Gemini Flash for translation, Gemini for TTS")
	} else {
		provider = gemini
		log.Printf("[ai] using Gemini for all AI tasks")
	}

	hub := room.NewHub()
	handler := signaling.NewHandler(hub, cfg, provider)

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(cfg.FrontendPath)))
	mux.HandleFunc("/ws", handler.ServeWS)
	mux.HandleFunc("/api/ice-config", handler.ICEConfig)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("[server] listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, withCORS(mux)); err != nil {
		log.Fatalf("[server] %v", err)
	}
}

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}
