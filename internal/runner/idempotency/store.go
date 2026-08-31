package idempotency

import (
	"errors"
	"sync"

	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
)

var ErrInProgress = errors.New("request already in progress")

type Store struct {
	mu      sync.Mutex
	running map[string]struct{}
	done    map[string]*runnerv1.ExecuteApprovedPlanResponse
}

func New() *Store {
	return &Store{running: map[string]struct{}{}, done: map[string]*runnerv1.ExecuteApprovedPlanResponse{}}
}
func (s *Store) Get(id string) (*runnerv1.ExecuteApprovedPlanResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.done[id]
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
}
