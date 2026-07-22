package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type VoiceProfile struct {
	ParticipantID string
	SampleData    []byte
	MIMEType      string
	CreatedAt     time.Time
}

type VoiceCloneRegistry struct {
	profiles map[string]*VoiceProfile
	mu       sync.RWMutex
	apiKey   string
	model    string
	http     *http.Client
}

func NewVoiceCloneRegistry(apiKey, model string) *VoiceCloneRegistry {
	return &VoiceCloneRegistry{
		profiles: make(map[string]*VoiceProfile),
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *VoiceCloneRegistry) Register(id string, data []byte, mimeType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles[id] = &VoiceProfile{ParticipantID: id, SampleData: data, MIMEType: mimeType, CreatedAt: time.Now()}
	log.Printf("[voice-clone] profile registered participant=%s bytes=%d", id, len(data))
}

func (r *VoiceCloneRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.profiles, id)
}

func (r *VoiceCloneRegistry) HasProfile(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.profiles[id]
	return ok
}

func (r *VoiceCloneRegistry) SynthesizeCloned(
	ctx context.Context,
	participantID, text, lang string,
	fallback func(ctx context.Context, text, lang, voiceID string) ([]byte, string, error),
) ([]byte, string, error) {
	r.mu.RLock()
	profile, ok := r.profiles[participantID]
	r.mu.RUnlock()
	if !ok {
		return fallback(ctx, text, lang, "")
	}
	audio, mime, err := r.synthesizeWithSample(ctx, text, profile)
	if err != nil {
		log.Printf("[voice-clone] failed (%v) — using generic TTS", err)
		return fallback(ctx, text, lang, "")
	}
	return audio, mime, nil
}

type cloneReq struct {
	Contents         []cloneContent `json:"contents"`
	GenerationConfig cloneGenCfg    `json:"generationConfig"`
}
type cloneContent struct{ Parts []clonePart `json:"parts"` }
type clonePart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineBlob `json:"inlineData,omitempty"`
}
type inlineBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}
type cloneGenCfg struct {
	ResponseModalities []string    `json:"responseModalities"`
	SpeechConfig       speechCfg   `json:"speechConfig"`
}

func (r *VoiceCloneRegistry) synthesizeWithSample(ctx context.Context, text string, p *VoiceProfile) ([]byte, string, error) {
	sampleB64 := base64.StdEncoding.EncodeToString(p.SampleData)
	payload := cloneReq{
		Contents: []cloneContent{{Parts: []clonePart{
			{InlineData: &inlineBlob{MIMEType: p.MIMEType, Data: sampleB64}},
			{Text: text},
		}}},
		GenerationConfig: cloneGenCfg{
			ResponseModalities: []string{"AUDIO"},
			SpeechConfig:       speechCfg{VoiceConfig: voiceCfg{}},
		},
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", r.model, r.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("clone api %d: %s", resp.StatusCode, string(raw))
	}
	var tr ttsResp
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, "", err
	}
	for _, c := range tr.Candidates {
		for _, pt := range c.Content.Parts {
			if pt.InlineData != nil && pt.InlineData.Data != "" {
				b, err := base64.StdEncoding.DecodeString(pt.InlineData.Data)
				if err != nil {
					return nil, "", err
				}
				return b, pt.InlineData.MIMEType, nil
			}
		}
	}
	return nil, "", fmt.Errorf("clone: no audio in response")
}
