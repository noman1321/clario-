package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const groqBaseURL = "https://api.groq.com/openai/v1"

type GroqProvider struct {
	apiKey     string
	sttModel   string
	transModel string
	client     *http.Client
}

func NewGroqProvider(apiKey, sttModel, transModel string) *GroqProvider {
	return &GroqProvider{
		apiKey:     apiKey,
		sttModel:   sttModel,
		transModel: transModel,
		client:     &http.Client{},
	}
}

// Transcribe uses Groq Whisper API.
func (g *GroqProvider) Transcribe(ctx context.Context, audioData []byte, mimeType, lang string) (string, error) {
	ext := mimeToExt(mimeType)
	filename := "audio." + ext

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("groq stt form file: %w", err)
	}
	if _, err = io.Copy(fw, bytes.NewReader(audioData)); err != nil {
		return "", fmt.Errorf("groq stt copy: %w", err)
	}
	_ = w.WriteField("model", g.sttModel)
	_ = w.WriteField("response_format", "json")
	_ = w.WriteField("temperature", "0")
	if lang != "" && lang != "auto" {
		_ = w.WriteField("language", lang)
	}
	w.Close()

	reqBody := buf.Bytes()
	contentType := w.FormDataContentType()

	// One retry on 429 — Groq free tier is 20 req/min, transient bursts are common.
	var body []byte
	var status int
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, groqBaseURL+"/audio/transcriptions", bytes.NewReader(reqBody))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
		req.Header.Set("Content-Type", contentType)

		resp, err := g.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("groq stt request: %w", err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		status = resp.StatusCode

		if status == http.StatusTooManyRequests && attempt == 0 {
			wait := retryAfter(resp.Header.Get("Retry-After"), 3500*time.Millisecond)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		break
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("groq stt %d: %s", status, body)
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("groq stt parse: %w", err)
	}

	text := strings.TrimSpace(result.Text)
	return text, nil
}

func retryAfter(header string, fallback time.Duration) time.Duration {
	if header != "" {
		if secs, err := strconv.ParseFloat(header, 64); err == nil && secs > 0 && secs < 30 {
			return time.Duration(secs*1000) * time.Millisecond
		}
	}
	return fallback
}

// Translate uses Groq Llama chat completions.
func (g *GroqProvider) Translate(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	prompt := fmt.Sprintf(
		"Translate the following %s text to %s.\nReturn ONLY the translated text. No notes, no explanations, no alternatives.\n\nText: %s",
		LangName(sourceLang), LangName(targetLang), text,
	)

	payload := map[string]any{
		"model": g.transModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
		"max_tokens":  512,
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, groqBaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq translate request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq translate %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("groq translate parse: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("groq translate: no choices")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

// Synthesize is not implemented on GroqProvider — use HybridProvider.
func (g *GroqProvider) Synthesize(_ context.Context, _, _, _ string) ([]byte, string, error) {
	return nil, "", fmt.Errorf("groq: synthesize not supported")
}

func mimeToExt(mime string) string {
	switch {
	case strings.Contains(mime, "webm"):
		return "webm"
	case strings.Contains(mime, "ogg"):
		return "ogg"
	case strings.Contains(mime, "mp4"), strings.Contains(mime, "m4a"):
		return "m4a"
	case strings.Contains(mime, "mpeg"), strings.Contains(mime, "mp3"):
		return "mp3"
	case strings.Contains(mime, "wav"):
		return "wav"
	default:
		return "webm"
	}
}
