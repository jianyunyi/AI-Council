package idempotency

import (
	"errors"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
	"sync"

	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
)

var ErrInProgress = errors.New("request already in progress")

type Store struct {
	mu      sync.Mutex
	running map[string]struct{}
	done    map[string]*runnerv1.ExecuteApprovedPlanResponse
	db      *gorm.DB
}

func New() *Store {
	return &Store{running: map[string]struct{}{}, done: map[string]*runnerv1.ExecuteApprovedPlanResponse{}}
}
func NewWithDB(db *gorm.DB) *Store { s := New(); s.db = db; return s }
func (s *Store) Get(id string) (*runnerv1.ExecuteApprovedPlanResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.done[id]
	if !ok && s.db != nil {
		var rec sqlite.ExecutionRecord
		if err := s.db.First(&rec, "request_id = ?", id).Error; err == nil {
			v = &runnerv1.ExecuteApprovedPlanResponse{}
			if protojson.Unmarshal(rec.ResponseJSON, v) == nil {
				s.done[id] = v
				ok = true
			}
		}
	}
	return v, ok
}
func (s *Store) Begin(id string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.done[id]; ok {
		return func() {}, nil
	}
	if _, ok := s.running[id]; ok {
		return nil, ErrInProgress
	}
	s.running[id] = struct{}{}
	return func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }, nil
}
func (s *Store) Complete(id string, response *runnerv1.ExecuteApprovedPlanResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, id)
	s.done[id] = response
	if s.db != nil {
		if raw, err := protojson.Marshal(response); err == nil {
			_ = s.db.Save(&sqlite.ExecutionRecord{RequestID: id, ResponseJSON: raw}).Error
		}
	}
}
