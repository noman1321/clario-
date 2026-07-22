package signaling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"polyglot-meet/ai"
	"polyglot-meet/config"
	"polyglot-meet/pipeline"
	"polyglot-meet/room"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Handler struct {
	hub         *room.Hub
	cfg         *config.Config
	provider    ai.AIProvider
	pipelinesMu sync.Mutex
	pipelines   map[string]*pipeline.Pipeline
}

func NewHandler(hub *room.Hub, cfg *config.Config, provider ai.AIProvider) *Handler {
	return &Handler{
		hub:       hub,
		cfg:       cfg,
		provider:  provider,
		pipelines: make(map[string]*pipeline.Pipeline),
	}
}

func (h *Handler) ICEConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	servers := []map[string]interface{}{
		{"urls": "stun:stun.l.google.com:19302"},
		{"urls": "stun:stun1.l.google.com:19302"},
	}
	if h.cfg.TURNHost != "" {
		servers = append(servers, map[string]interface{}{
			"urls":       fmt.Sprintf("turn:%s:%s", h.cfg.TURNHost, h.cfg.TURNPort),
			"username":   h.cfg.TURNUser,
			"credential": h.cfg.TURNPass,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"iceServers": servers})
}

func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &wsClient{conn: conn, handler: h, ctx: ctx, cancel: cancel}
	c.run()
}

type wsClient struct {
	conn        *websocket.Conn
	handler     *Handler
	ctx         context.Context
	cancel      context.CancelFunc
	participant *room.Participant
	roomRef     *room.Room
	sendC       chan []byte
}

func (c *wsClient) run() {
	c.sendC = make(chan []byte, 512)
	defer func() {
		c.cleanup()
		c.conn.Close()
	}()
	go c.writePump()
	c.readPump()
}

func (c *wsClient) readPump() {
	c.conn.SetReadLimit(10 * 1024 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[ws] read error: %v", err)
			}
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if err := c.handle(msg); err != nil {
			log.Printf("[ws] handle type=%s error: %v", msg.Type, err)
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case data, ok := <-c.sendC:
			if !ok {
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}

func (c *wsClient) handle(msg WSMessage) error {
	switch msg.Type {
	case "join":
		return c.handleJoin(msg.Data)
	case "offer", "answer", "ice":
		return c.handleRelay(msg)
	case "lang_change":
		return c.handleLangChange(msg.Data)
	case "audio_chunk":
		return c.handleAudioChunk(msg.Data)
	case "admit":
		return c.handleAdmit(msg.Data)
	case "chat_message":
		return c.handleChatMessage(msg.Data)
	case "voice_clone_consent":
		return c.handleVoiceCloneConsent(msg.Data)
	case "voice_sample":
		return c.handleVoiceSample(msg.Data)
	case "leave":
		c.cleanup()
	}
	return nil
}

// ── Join ────────────────────────────────────────────────────────────────────

type joinPayload struct {
	RoomID     string `json:"roomId"`
	Name       string `json:"name"`
	TargetLang string `json:"targetLang"`
	SourceLang string `json:"sourceLang"`
}

func (c *wsClient) handleJoin(data json.RawMessage) error {
	var p joinPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.RoomID == "" || p.Name == "" {
		return fmt.Errorf("missing roomId or name")
	}
	if p.TargetLang == "" {
		p.TargetLang = "en"
	}
	if p.SourceLang == "" {
		p.SourceLang = "en"
	}

	participant := room.NewParticipant(p.Name, p.RoomID)
	participant.Conn = c.conn
	participant.Send = c.sendC
	participant.SetLangs(p.TargetLang, p.SourceLang)

	r := c.handler.hub.GetOrCreate(p.RoomID)
	c.participant = participant
	c.roomRef = r

	// First person in the room → becomes admin, enters immediately.
	// Anyone after → goes to waiting room until admin admits them.
	if r.Size() == 0 {
		c.fullyAdmit(participant, r)
		return nil
	}

	// Put in waiting room
	r.AddWaiting(participant)
	log.Printf("[ws] waiting room=%s participant=%s", p.RoomID, p.Name)

	waitMsg, _ := json.Marshal(map[string]interface{}{
		"type":          "waiting",
		"participantId": participant.ID,
	})
	select {
	case c.sendC <- waitMsg:
	default:
	}

	// Notify admin
	if admin, ok := r.GetAdmin(); ok {
		req, _ := json.Marshal(map[string]interface{}{
			"type":          "admit_request",
			"participantId": participant.ID,
			"name":          participant.Name,
		})
		select {
		case admin.Send <- req:
		default:
		}
	}
	return nil
}

// fullyAdmit moves participant into the room and sends joined/participant_joined messages.
func (c *wsClient) fullyAdmit(participant *room.Participant, r *room.Room) {
	existing := r.All()
	r.Add(participant)
	r.SetAdmin(participant.ID)
	c.handler.ensurePipeline(r)

	log.Printf("[ws] joined room=%s participant=%s (%s)", r.ID, participant.Name, participant.ID)

	infos := make([]room.ParticipantInfo, 0, len(existing))
	for _, ep := range existing {
		infos = append(infos, ep.Info())
	}
	joined, _ := json.Marshal(map[string]interface{}{
		"type":          "joined",
		"participantId": participant.ID,
		"participants":  infos,
		"isAdmin":       r.IsAdmin(participant.ID),
	})
	select {
	case participant.Send <- joined:
	default:
	}

	newMsg, _ := json.Marshal(map[string]interface{}{
		"type":        "participant_joined",
		"participant": participant.Info(),
	})
	for _, ep := range existing {
		select {
		case ep.Send <- newMsg:
		default:
		}
	}
}

// ── Admit / Reject ──────────────────────────────────────────────────────────

type admitPayload struct {
	ParticipantID string `json:"participantId"`
	Admit         bool   `json:"admit"`
}

func (c *wsClient) handleAdmit(data json.RawMessage) error {
	if c.participant == nil || c.roomRef == nil {
		return nil
	}
	var p admitPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}

	waiting, ok := c.roomRef.GetWaiting(p.ParticipantID)
	if !ok {
		return nil
	}
	c.roomRef.RemoveWaiting(p.ParticipantID)

	if !p.Admit {
		rejected, _ := json.Marshal(map[string]interface{}{"type": "rejected"})
		select {
		case waiting.Send <- rejected:
		default:
		}
		log.Printf("[ws] rejected room=%s participant=%s", c.roomRef.ID, waiting.Name)
		return nil
	}

	// Admit: add to active room and send joined
	existing := c.roomRef.All()
	c.roomRef.Add(waiting)
	c.handler.ensurePipeline(c.roomRef)
	log.Printf("[ws] admitted room=%s participant=%s", c.roomRef.ID, waiting.Name)

	infos := make([]room.ParticipantInfo, 0, len(existing))
	for _, ep := range existing {
		infos = append(infos, ep.Info())
	}
	joined, _ := json.Marshal(map[string]interface{}{
		"type":          "joined",
		"participantId": waiting.ID,
		"participants":  infos,
		"isAdmin":       false,
	})
	select {
	case waiting.Send <- joined:
	default:
	}

	newMsg, _ := json.Marshal(map[string]interface{}{
		"type":        "participant_joined",
		"participant": waiting.Info(),
	})
	for _, ep := range existing {
		select {
		case ep.Send <- newMsg:
		default:
		}
	}
	return nil
}

// ── Chat ────────────────────────────────────────────────────────────────────

type chatPayload struct {
	Text string `json:"text"`
}

func (c *wsClient) handleChatMessage(data json.RawMessage) error {
	if c.participant == nil || c.roomRef == nil {
		return nil
	}
	var p chatPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return nil
	}

	msg, _ := json.Marshal(map[string]interface{}{
		"type":       "chat_message",
		"senderId":   c.participant.ID,
		"senderName": c.participant.Name,
		"text":       text,
		"ts":         time.Now().UnixMilli(),
	})

	for _, ep := range c.roomRef.All() {
		select {
		case ep.Send <- msg:
		default:
		}
	}
	return nil
}

// ── Relay (WebRTC signals) ──────────────────────────────────────────────────

type relayPayload struct {
	TargetID string          `json:"targetId"`
	Data     json.RawMessage `json:"data"`
}

func (c *wsClient) handleRelay(msg WSMessage) error {
	if c.roomRef == nil || c.participant == nil {
		return nil
	}
	var p relayPayload
	if err := json.Unmarshal(msg.Data, &p); err != nil {
		return err
	}
	target, ok := c.roomRef.Get(p.TargetID)
	if !ok {
		return nil
	}
	fwd, _ := json.Marshal(map[string]interface{}{
		"type":     msg.Type,
		"senderId": c.participant.ID,
		"data":     p.Data,
	})
	select {
	case target.Send <- fwd:
	default:
	}
	return nil
}

// ── Lang change ─────────────────────────────────────────────────────────────

type langPayload struct {
	TargetLang string `json:"targetLang"`
	SourceLang string `json:"sourceLang"`
}

func (c *wsClient) handleLangChange(data json.RawMessage) error {
	if c.participant == nil {
		return nil
	}
	var p langPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c.participant.SetLangs(p.TargetLang, p.SourceLang)
	return nil
}

// ── Audio chunk ─────────────────────────────────────────────────────────────

type audioChunkPayload struct {
	SourceLang string `json:"sourceLang"`
	MIMEType   string `json:"mimeType"`
	Data       string `json:"data"`
}

func (c *wsClient) handleAudioChunk(data json.RawMessage) error {
	if c.participant == nil || c.roomRef == nil {
		return nil
	}
	var p audioChunkPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	audioBytes, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return err
	}
	mimeType := p.MIMEType
	if mimeType == "" {
		mimeType = "audio/webm"
	}
	c.handler.pipelinesMu.Lock()
	pl, ok := c.handler.pipelines[c.roomRef.ID]
	c.handler.pipelinesMu.Unlock()
	if ok {
		pl.Push(pipeline.AudioChunk{
			SpeakerID:  c.participant.ID,
			SourceLang: p.SourceLang,
			MIMEType:   mimeType,
			Data:       audioBytes,
		})
	}
	return nil
}

// ── Voice clone (Phase 3 stubs) ─────────────────────────────────────────────

type consentPayload struct{ Consented bool `json:"consented"` }

func (c *wsClient) handleVoiceCloneConsent(data json.RawMessage) error {
	if c.participant == nil {
		return nil
	}
	var p consentPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c.participant.SetConsent(p.Consented)
	if !p.Consented && c.roomRef != nil {
		c.handler.pipelinesMu.Lock()
		if pl, ok := c.handler.pipelines[c.roomRef.ID]; ok {
			pl.RemoveVoiceProfile(c.participant.ID)
		}
		c.handler.pipelinesMu.Unlock()
	}
	return nil
}

type voiceSamplePayload struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

func (c *wsClient) handleVoiceSample(data json.RawMessage) error {
	if c.participant == nil || c.roomRef == nil || !c.participant.GetConsent() {
		return nil
	}
	var p voiceSamplePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	audioBytes, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return err
	}
	c.handler.pipelinesMu.Lock()
	pl, ok := c.handler.pipelines[c.roomRef.ID]
	c.handler.pipelinesMu.Unlock()
	if ok {
		pl.RegisterVoiceProfile(c.participant.ID, audioBytes, p.MIMEType)
	}
	return nil
}

// ── Cleanup ─────────────────────────────────────────────────────────────────

func (c *wsClient) cleanup() {
	if c.participant == nil || c.roomRef == nil {
		return
	}
	p := c.participant
	r := c.roomRef
	c.participant = nil
	c.roomRef = nil

	// Remove from waiting room (if still waiting)
	r.RemoveWaiting(p.ID)

	// Remove from active participants (Remove also re-elects admin)
	r.Remove(p.ID)
	log.Printf("[ws] left room=%s participant=%s", r.ID, p.Name)

	leftMsg, _ := json.Marshal(map[string]interface{}{
		"type":          "participant_left",
		"participantId": p.ID,
	})
	for _, ep := range r.All() {
		select {
		case ep.Send <- leftMsg:
		default:
		}
	}
	if r.Size() == 0 {
		c.handler.removePipeline(r.ID)
		c.handler.hub.Cleanup(r.ID)
	}
	c.cancel()
}

func (h *Handler) ensurePipeline(r *room.Room) {
	h.pipelinesMu.Lock()
	defer h.pipelinesMu.Unlock()
	if _, ok := h.pipelines[r.ID]; ok {
		return
	}
	pl := pipeline.New(r, h.provider, h.cfg.GeminiAPIKey, h.cfg.TTSModel)
	pl.Start(context.Background())
	h.pipelines[r.ID] = pl
	log.Printf("[pipeline] started for room=%s", r.ID)
}

func (h *Handler) removePipeline(roomID string) {
	h.pipelinesMu.Lock()
	defer h.pipelinesMu.Unlock()
	if pl, ok := h.pipelines[roomID]; ok {
		pl.Stop()
		delete(h.pipelines, roomID)
	}
}
