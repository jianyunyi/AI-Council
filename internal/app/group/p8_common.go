package group

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P8 shared helpers. Transaction discipline matches the landed
// P6/P7 code: multi-event operations run inside the per-group lock +
// one GroupRepo.Tx via reviewCommit (the landed equivalent of the
// draft's p8Locked), using tx-bound repos, and publish their events
// only after commit, in allocation order. Event seqs stay gapless:
// NextSeqs(false) allocates ONLY an event seq; NextSeqs(true) allocates
// a message seq + event seq pair that must be used together.

// mirrorRegistry tracks the running mirror goroutines (one per
// in-flight push-down task). Defined here; the loop lives in mirror.go.
type mirrorRegistry struct {
	mu   sync.Mutex
	runs map[string]context.CancelFunc // taskID -> cancel
	root context.Context
}

func newMirrorRegistry() *mirrorRegistry {
	return &mirrorRegistry{runs: map[string]context.CancelFunc{}, root: context.Background()}
}

// start claims a mirror slot for key (a pushdown id); false when a
// mirror is already running for it.
func (r *mirrorRegistry) start(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runs[key]; ok {
		return false
	}
	r.runs[key] = func() {}
	return true
}

func (r *mirrorRegistry) done(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, key)
}

// SetMirrorContext ties mirror goroutines to server shutdown (cmd
// wiring passes the worker's signal context).
func (s *Service) SetMirrorContext(ctx context.Context) {
	if s.mirrors != nil && ctx != nil {
		s.mirrors.root = ctx
	}
}

func (s *Service) pd() *sqlite.PushdownRepo { return sqlite.NewPushdownRepo(s.repo.DB()) }

// canApprove is the D1 rule: a human, ACTIVE member with group role
// owner or operator. The requirement's creator approves only while
// holding one of those roles. AI members, viewers and the legacy
// anonymous caller ("legacy" is the transport layer's no-auth user id)
// can never approve. There is no second approver in v1.
func canApprove(a Actor, m *sqlite.ChatGroupMemberRecord) bool {
	if a.UserID == "" || a.UserID == "legacy" || m == nil || m.MemberType != MemberUser || m.RemovedAt != nil {
		return false
	}
	return m.GroupRole == RoleOwner || m.GroupRole == RoleOperator
}

// passingRound returns the closed, passed round over the requirement's
// CURRENT proposal, or nil. P9 (C2): result rounds also carry the
// current ProposalID, so the match must be stage-filtered — a passed
// RESULT round must never satisfy the proposal gate.
func (s *Service) passingRound(ctx context.Context, req *sqlite.GroupRequirementRecord) (*sqlite.ReviewRoundRecord, error) {
	rounds, err := s.reviewRepo().ListRounds(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	for i := len(rounds) - 1; i >= 0; i-- {
		r := &rounds[i]
		if r.Stage == StageResult {
			continue
		}
		if r.ProposalID == req.CurrentProposalID && r.Status == RoundClosed && r.Outcome == OutcomePassed {
			return r, nil
		}
	}
	return nil, nil
}

// p8TransitionTx CASes a requirement state inside tx and appends the
// requirement.state_changed event next to it (the landed
// UpdateRequirementState does not append events itself, W4).
// terminal_at is set exactly for the terminal states (isTerminal);
// since P9 (C1) execution_failed is NOT one of them — a result
// submission still follows it — and neither is executed.
func (s *Service) p8TransitionTx(ctx context.Context, tx *sqlite.GroupRepo, req *sqlite.GroupRequirementRecord, from []string, to string, extra map[string]any, at time.Time) (Event, bool, error) {
	txReq := sqlite.NewRequirementRepo(tx.DB())
	fields := map[string]any{"state": to, "state_changed_at": at, "updated_at": at}
	for k, v := range extra {
		fields[k] = v
	}
	if isTerminal(to) {
		fields["terminal_at"] = at
	}
	ok, err := txReq.UpdateRequirementState(ctx, req.ID, from, req.Version, fields)
	if err != nil || !ok {
		return Event{}, ok, err
	}
	failureCode, _ := extra["failure_code"].(string)
	ev, err := rvAppendEvent(ctx, tx, req.GroupID, "requirement.state_changed",
		RequirementEventData{RequirementID: req.ID, State: to, Version: req.Version + 1, FailureCode: failureCode}, at)
	if err != nil {
		return Event{}, true, err
	}
	return ev, true, nil
}

// p8SystemMessage writes a system card message inside tx; its
// message.created event uses the SAME NextSeqs(true) allocation (the
// rvPostSystemTx pattern, with a P8 message kind).
func (s *Service) p8SystemMessage(ctx context.Context, tx *sqlite.GroupRepo, gid, rid, kind, body string, payloadMap map[string]any, at time.Time) (Event, error) {
	payload, _ := json.Marshal(payloadMap)
	msgSeq, evSeq, err := tx.NextSeqs(ctx, gid, true, at)
	if err != nil {
		return Event{}, err
	}
	msg := &sqlite.ChatMessageRecord{
		ID: newID("msg_"), GroupID: gid, Seq: msgSeq, AuthorType: AuthorSystem,
		Kind: kind, BodyMD: body, Payload: payload, RequirementID: rid,
		Status: "final", CreatedAt: at,
	}
	if err := tx.CreateMessage(ctx, msg); err != nil {
		return Event{}, err
	}
	rec, ev, err := buildEvent(gid, evSeq, "message.created", messageView(*msg, nil, nil), at)
	if err != nil {
		return Event{}, err
	}
	if err := tx.AppendEvent(ctx, rec); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// memberHandle resolves a member's display handle for card text.
func (s *Service) memberHandle(ctx context.Context, gid, memberID string) string {
	authors, _, err := s.authorMap(ctx, gid)
	if err != nil {
		return ""
	}
	return authors[memberID].Handle
}
