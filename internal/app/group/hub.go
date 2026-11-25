package group

import (
	"encoding/json"
	"sync"
	"time"
)

// Event is the SSE envelope: {seq, type, ts, data}.
type Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	At   time.Time       `json:"ts"`
	Data json.RawMessage `json:"data"`
}

// Subscription receives live group events. C is closed when the subscriber
// falls behind, is removed from the group, or Close is called. The client then
// reconnects with Last-Event-ID and replays from the database.
type Subscription struct {
	C       <-chan Event
	ch      chan Event
	groupID string
	userID  string
	hub     *Hub
	closed  bool
}

func (s *Subscription) Close() { s.hub.remove(s) }

type Hub struct {
	mu     sync.Mutex
	buffer int
	subs   map[string]map[*Subscription]struct{}
}

func NewHub(buffer int) *Hub {
	if buffer <= 0 {
		buffer = 256
	}
	return &Hub{buffer: buffer, subs: map[string]map[*Subscription]struct{}{}}
}

func (h *Hub) Subscribe(groupID, userID string) *Subscription {
	ch := make(chan Event, h.buffer)
	s := &Subscription{C: ch, ch: ch, groupID: groupID, userID: userID, hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[groupID] == nil {
		h.subs[groupID] = map[*Subscription]struct{}{}
	}
	h.subs[groupID][s] = struct{}{}
	return s
}

// closeLocked must be called with h.mu held.
func (h *Hub) closeLocked(s *Subscription) {
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
	if set := h.subs[s.groupID]; set != nil {
		delete(set, s)
		if len(set) == 0 {
			delete(h.subs, s.groupID)
		}
	}
}

func (h *Hub) remove(s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked(s)
}

// Publish never blocks. A subscriber whose buffer is full is disconnected.
func (h *Hub) Publish(groupID string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[groupID] {
		select {
		case s.ch <- ev:
		default:
			h.closeLocked(s)
		}
	}
}

// Kick disconnects every subscription of userID in groupID (membership revoked).
func (h *Hub) Kick(groupID, userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[groupID] {
		if s.userID == userID {
			h.closeLocked(s)
		}
	}
}

func (h *Hub) SubscriberCount(groupID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[groupID])
}
