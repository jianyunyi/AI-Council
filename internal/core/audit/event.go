package audit

import "time"

type Event struct {
	ID        string
	RunID     string
	Sequence  uint64
	Type      string
	Actor     string
	Detail    string
	CreatedAt time.Time
}
