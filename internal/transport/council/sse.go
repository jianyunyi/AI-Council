package council

import (
	"encoding/json"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"net/http"
	"strconv"
)

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	t := a.tasks[pathvar.Vars(r)["id"]]
	if t == nil {
		a.mu.Unlock()
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if q := r.URL.Query().Get("after"); q != "" {
		after, _ = strconv.ParseInt(q, 10, 64)
	}
	events := append([]Event(nil), t.Events...)
	a.mu.Unlock()
	if a.eventRepo != nil {
		if persisted, err := a.eventRepo.After(r.Context(), t.ID, after, 200); err == nil {
			events = events[:0]
			for _, e := range persisted {
				events = append(events, Event{ID: e.Sequence, Type: e.Type, Data: json.RawMessage(e.Data), CreatedAt: e.CreatedAt})
			}
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	for _, e := range events {
		if e.ID <= after {
			continue
		}
		data, _ := json.Marshal(e.Data)
		_, _ = w.Write([]byte("id: " + strconv.FormatInt(e.ID, 10) + "\nevent: " + e.Type + "\ndata: " + string(data) + "\n\n"))
		flusher.Flush()
	}
	_, _ = w.Write([]byte(": heartbeat\n\n"))
	flusher.Flush()
}
