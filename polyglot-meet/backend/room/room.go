package room

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type ParticipantInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TargetLang string `json:"targetLang"`
	SourceLang string `json:"sourceLang"`
}

type Participant struct {
	ID                string
	Name              string
	TargetLang        string
	SourceLang        string
	Conn              *websocket.Conn
	Send              chan []byte
	RoomID            string
	JoinedAt          time.Time
	VoiceCloneConsent bool
	mu                sync.RWMutex
}

func NewParticipant(name, roomID string) *Participant {
	return &Participant{
		ID:         uuid.New().String(),
		Name:       name,
		TargetLang: "en",
		SourceLang: "en",
		Send:       make(chan []byte, 512),
		RoomID:     roomID,
		JoinedAt:   time.Now(),
	}
}

func (p *Participant) SetLangs(targetLang, sourceLang string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.TargetLang = targetLang
	p.SourceLang = sourceLang
}

func (p *Participant) GetLangs() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.TargetLang, p.SourceLang
}

func (p *Participant) SetConsent(consented bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.VoiceCloneConsent = consented
}

func (p *Participant) GetConsent() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.VoiceCloneConsent
}

func (p *Participant) Info() ParticipantInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return ParticipantInfo{
		ID:         p.ID,
		Name:       p.Name,
		TargetLang: p.TargetLang,
		SourceLang: p.SourceLang,
	}
}

// Room holds active participants, a waiting room queue, and tracks the admin.
type Room struct {
	ID           string
	adminID      string
	participants map[string]*Participant
	waiting      map[string]*Participant
	mu           sync.RWMutex
}

func newRoom(id string) *Room {
	return &Room{
		ID:           id,
		participants: make(map[string]*Participant),
		waiting:      make(map[string]*Participant),
	}
}

func (r *Room) Add(p *Participant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.participants[p.ID] = p
}

func (r *Room) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.participants, id)
	if r.adminID == id {
		r.adminID = ""
		for next := range r.participants {
			r.adminID = next
			break
		}
	}
}

func (r *Room) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.participants)
}

func (r *Room) All() []*Participant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Participant, 0, len(r.participants))
	for _, p := range r.participants {
		out = append(out, p)
	}
	return out
}

func (r *Room) Others(myID string) []*Participant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Participant, 0)
	for id, p := range r.participants {
		if id != myID {
			out = append(out, p)
		}
	}
	return out
}

func (r *Room) Get(id string) (*Participant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.participants[id]
	return p, ok
}

// SetAdmin sets the admin if none is set yet.
func (r *Room) SetAdmin(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adminID == "" {
		r.adminID = id
	}
}

// GetAdmin returns the current admin participant.
func (r *Room) GetAdmin() (*Participant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.adminID == "" {
		return nil, false
	}
	p, ok := r.participants[r.adminID]
	return p, ok
}

// IsAdmin reports whether id is the room admin.
func (r *Room) IsAdmin(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adminID == id
}

// AddWaiting places a participant in the waiting room.
func (r *Room) AddWaiting(p *Participant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waiting[p.ID] = p
}

// RemoveWaiting removes a participant from the waiting room (safe to call even if absent).
func (r *Room) RemoveWaiting(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.waiting, id)
}

// GetWaiting retrieves a waiting participant.
func (r *Room) GetWaiting(id string) (*Participant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.waiting[id]
	return p, ok
}

// IsWaiting reports whether a participant is in the waiting room.
func (r *Room) IsWaiting(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.waiting[id]
	return ok
}

type Hub struct {
	rooms map[string]*Room
	mu    sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]*Room)}
}

func (h *Hub) GetOrCreate(roomID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[roomID]; ok {
		return r
	}
	r := newRoom(roomID)
	h.rooms[roomID] = r
	return r
}

func (h *Hub) Cleanup(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[roomID]; ok && r.Size() == 0 {
		delete(h.rooms, roomID)
	}
}

func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}
