package group

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Dispatch fan-out, requirement queries, retry and cancel (P6 §3 of the
// plan, adapted to the landed P5 service):
//
//   - Mentions arrive as []sqlite.ChatMentionRecord from resolveMentions;
//     P5 records every mention with intent "reference", so a "call" is
//     derived here: a resolved mention whose target member is an AI member.
//   - PostMessage is human-only in the landed service (the author is the
//     caller's own member row), so AI-authored dispatch is inert by
//     construction — an AI member can never reach this code path.
//   - Card messages are authored by the system (AuthorType "system", no
//     member row), which the UI renders as 系统.

type dispatchTarget struct {
	MemberID  string
	AIModelID string
	Handle    string
}

// extractCallSpecs returns the distinct AI members a dispatch message
// calls: resolved mentions targeting AI members, first-mention order.
func extractCallSpecs(mentions []sqlite.ChatMentionRecord, authors map[string]MemberView) []dispatchTarget {
	seen := map[string]bool{}
	var out []dispatchTarget
	for _, mn := range mentions {
		if mn.Resolution != MentionResolved || mn.TargetMemberID == "" {
			continue
		}
		view, ok := authors[mn.TargetMemberID]
		if !ok || view.MemberType != MemberAI || view.AIModelID == "" {
			continue
		}
		if seen[mn.TargetMemberID] {
			continue
		}
		seen[mn.TargetMemberID] = true
		out = append(out, dispatchTarget{MemberID: mn.TargetMemberID, AIModelID: view.AIModelID, Handle: view.Handle})
	}
	return out
}

// validateAssignee checks a dispatch target can actually be invoked: the
// member is a live AI member, the model is active, and its dedicated
// workspace finished provisioning (P4 states).
func (s *Service) validateAssignee(ctx context.Context, memberID string, authors map[string]MemberView) (MemberView, AIModelInfo, error) {
	view, ok := authors[memberID]
	if !ok || view.MemberType != MemberAI || view.RemovedAt != nil {
		return MemberView{}, AIModelInfo{}, errAssigneeNotActive
	}
	info, err := s.models.GetAIModel(ctx, view.AIModelID)
	if err != nil {
		if errors.Is(err, ErrDirectoryNotFound) {
			return MemberView{}, AIModelInfo{}, errAIModelNotFound
		}
		return MemberView{}, AIModelInfo{}, err
	}
	if info.Status != "active" {
		return MemberView{}, AIModelInfo{}, errAssigneeNotActive
	}
	if info.WorkspaceStatus != "ready" {
		return MemberView{}, AIModelInfo{}, errAssigneeWorkspace
	}
	return view, info, nil
}

// dispatchFanout creates one requirement + job + dispatch card per target,
// inside the caller's transaction (the anchor message is already written).
// Returns the requirement IDs and the card events in creation order.
func (s *Service) dispatchFanout(ctx context.Context, tx *sqlite.GroupRepo, g *sqlite.ChatGroupRecord, author *sqlite.ChatGroupMemberRecord, anchorMsgID, body string, specs []dispatchTarget, authors map[string]MemberView, at time.Time) ([]string, []Event, error) {
	txReq := sqlite.NewRequirementRepo(tx.DB())
	ids := make([]string, 0, len(specs))
	evs := make([]Event, 0, len(specs))
	for _, spec := range specs {
		id, ev, err := s.createRequirementTx(ctx, tx, txReq, g, author, anchorMsgID, spec, body, authors, at)
		if err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		evs = append(evs, ev)
	}
	return ids, evs, nil
}

func (s *Service) createRequirementTx(ctx context.Context, tx *sqlite.GroupRepo, txReq *sqlite.RequirementRepo, g *sqlite.ChatGroupRecord, author *sqlite.ChatGroupMemberRecord, anchorMsgID string, spec dispatchTarget, body string, authors map[string]MemberView, at time.Time) (string, Event, error) {
	reqID := newID("req_")
	title := truncateRunes(firstLine(body), 120)
	rec := &sqlite.GroupRequirementRecord{
		ID: reqID, GroupID: g.ID, AnchorMessageID: anchorMsgID,
		RequestedByUserID: author.UserID, RequestedByMemberID: author.ID,
		AssigneeMemberID: spec.MemberID, AssigneeAIModelID: spec.AIModelID, AssigneeHandle: spec.Handle,
		Title: title, BodyMD: body, State: ReqDispatched, Version: 1,
		CreatedAt: at, UpdatedAt: at, StateChangedAt: at,
	}
	if err := txReq.CreateRequirement(ctx, rec); err != nil {
		return "", Event{}, err
	}
	job := &sqlite.GroupJobRecord{
		ID: newID("job_"), GroupID: g.ID, RequirementID: reqID, Kind: JobKindPropose,
		State: "queued", SerialKey: g.ID + ":" + spec.MemberID, MaxAttempts: 3,
		AvailableAt: at, CreatedAt: at, UpdatedAt: at,
	}
	if err := txReq.EnqueueJob(ctx, job); err != nil {
		return "", Event{}, err
	}
	// Dispatch card: a system message in the anchor's stream position,
	// carrying the requirement id; the UI renders the live card from the
	// requirement endpoints + requirement.state_changed events.
	payload, _ := json.Marshal(map[string]any{
		"requirement_id": reqID, "assignee_member_id": spec.MemberID,
		"assignee_ai_model_id": spec.AIModelID, "state": ReqDispatched,
	})
	msgSeq, evSeq, err := tx.NextSeqs(ctx, g.ID, true, at)
	if err != nil {
		return "", Event{}, err
	}
	card := &sqlite.ChatMessageRecord{
		ID: newID("msg_"), GroupID: g.ID, Seq: msgSeq, AuthorType: AuthorSystem,
		Kind: KindDispatchCard, BodyMD: fmt.Sprintf("Requirement dispatched to @%s: %s", spec.Handle, title),
		Payload: payload, RequirementID: reqID, ReplyToMessageID: anchorMsgID,
		Status: "final", CreatedAt: at,
	}
	if err := tx.CreateMessage(ctx, card); err != nil {
		return "", Event{}, err
	}
	view := messageView(*card, nil, authors)
	evRec, ev, err := buildEvent(g.ID, evSeq, "message.created", view, at)
	if err != nil {
		return "", Event{}, err
	}
	if err := tx.AppendEvent(ctx, evRec); err != nil {
		return "", Event{}, err
	}
	return reqID, ev, nil
}

// ---- views ----

func (s *Service) requirementView(ctx context.Context, rec *sqlite.GroupRequirementRecord) (RequirementView, error) {
	v := RequirementView{
		ID: rec.ID, GroupID: rec.GroupID, AnchorMessageID: rec.AnchorMessageID,
		RequestedBy: rec.RequestedByUserID, RequestedByMember: rec.RequestedByMemberID,
		AssigneeMemberID: rec.AssigneeMemberID, AssigneeAIModelID: rec.AssigneeAIModelID,
		AssigneeHandle: rec.AssigneeHandle, Title: rec.Title, BodyMD: rec.BodyMD,
		State: rec.State, FailureCode: rec.FailureCode, FailureDetail: rec.FailureDetail,
		Version: rec.Version, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
		StateChangedAt: rec.StateChangedAt, ProposedAt: rec.ProposedAt, TerminalAt: rec.TerminalAt,
	}
	if len(rec.AcceptanceJSON) > 0 {
		_ = json.Unmarshal(rec.AcceptanceJSON, &v.Acceptance)
	}
	if rec.CurrentProposalID != "" {
		if p, err := s.reqRepo.GetProposal(ctx, rec.CurrentProposalID); err == nil {
			pv := &ProposalView{ID: p.ID, Attempt: p.Attempt, Summary: p.Summary, CreatedAt: p.CreatedAt}
			_ = json.Unmarshal(p.PlanJSON, &pv.Plan)
			v.Proposal = pv
		}
	}
	if job, err := s.reqRepo.GetJobForRequirement(ctx, rec.ID); err == nil {
		v.Job = &JobView{State: job.State, Attempts: job.Attempts}
	}
	return v, nil
}

// ---- queries ----

func encodeReqCursor(rec sqlite.GroupRequirementRecord) string {
	raw := strconv.FormatInt(rec.CreatedAt.UnixNano(), 10) + "|" + rec.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeReqCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", errInvalidCursor
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errInvalidCursor
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || parts[1] == "" {
		return time.Time{}, "", errInvalidCursor
	}
	return time.Unix(0, ns).UTC(), parts[1], nil
}

func (s *Service) ListRequirements(ctx context.Context, a Actor, gid string, in ListRequirementsInput) (*RequirementPage, error) {
	if _, _, err := s.access(ctx, a, gid); err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	f := sqlite.RequirementFilter{GroupID: gid, State: in.State, AssigneeMemberID: in.AssigneeMemberID, Limit: limit}
	if in.Cursor != "" {
		ct, cid, err := decodeReqCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		f.CursorCreatedAt, f.CursorID = ct, cid
	}
	recs, more, err := s.reqRepo.ListRequirements(ctx, f)
	if err != nil {
		return nil, err
	}
	page := &RequirementPage{Items: make([]RequirementView, 0, len(recs))}
	for i := range recs {
		v, err := s.requirementView(ctx, &recs[i])
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, v)
	}
	if more && len(recs) > 0 {
		page.NextCursor = encodeReqCursor(recs[len(recs)-1])
	}
	return page, nil
}

func (s *Service) GetRequirement(ctx context.Context, a Actor, gid, rid string) (*RequirementView, error) {
	if _, _, err := s.access(ctx, a, gid); err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	v, err := s.requirementView(ctx, rec)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ---- retry / cancel ----

func (s *Service) RetryRequirement(ctx context.Context, a Actor, gid, rid string) (*RequirementView, error) {
	_, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if rec.RequestedByUserID != a.UserID && !canManage(a, m) {
		return nil, errForbidden
	}
	if rec.State != ReqProposalFailed && rec.State != ReqFailed {
		return nil, errRequirementState
	}
	now := s.now()
	_, err = s.mutate(ctx, gid, "requirement.state_changed", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		txReq := sqlite.NewRequirementRepo(tx.DB())
		ok, err := txReq.UpdateRequirementState(ctx, rid,
			[]string{ReqProposalFailed, ReqFailed}, rec.Version, map[string]any{
				"state": ReqDispatched, "failure_code": "", "failure_detail": "",
				"current_proposal_id": "", "terminal_at": nil,
				"state_changed_at": now, "updated_at": now,
			})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errRequirementGone
		}
		job := &sqlite.GroupJobRecord{
			ID: newID("job_"), GroupID: gid, RequirementID: rid, Kind: JobKindPropose,
			State: "queued", SerialKey: gid + ":" + rec.AssigneeMemberID, MaxAttempts: 3,
			AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := txReq.EnqueueJob(ctx, job); err != nil {
			return nil, err
		}
		return RequirementEventData{RequirementID: rid, State: ReqDispatched, Version: rec.Version + 1}, nil
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, a.UserID, "group.requirement_retried", gid, "group_requirement", rid, nil)
	return s.GetRequirement(ctx, a, gid, rid)
}

func (s *Service) CancelRequirement(ctx context.Context, a Actor, gid, rid, reason string) (*RequirementView, error) {
	_, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if rec.RequestedByUserID != a.UserID && !canManage(a, m) {
		return nil, errForbidden
	}
	switch rec.State {
	case ReqDispatched, ReqProposing, ReqProposed:
	default:
		return nil, errRequirementState
	}
	now := s.now()
	_, err = s.mutate(ctx, gid, "requirement.state_changed", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		txReq := sqlite.NewRequirementRepo(tx.DB())
		ok, err := txReq.UpdateRequirementState(ctx, rid,
			[]string{ReqDispatched, ReqProposing, ReqProposed}, rec.Version, map[string]any{
				"state": ReqCancelled, "cancelled_by_user_id": a.UserID,
				"cancelled_by_member_id": memberIDOf(m), "cancel_reason": truncate(reason, 2000),
				"terminal_at": now, "state_changed_at": now, "updated_at": now,
			})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errRequirementGone
		}
		if err := txReq.CancelQueuedJobsForRequirement(ctx, rid, now); err != nil {
			return nil, err
		}
		return RequirementEventData{RequirementID: rid, State: ReqCancelled, Version: rec.Version + 1}, nil
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, a.UserID, "group.requirement_cancelled", gid, "group_requirement", rid, map[string]any{"reason": truncate(reason, 200)})
	return s.GetRequirement(ctx, a, gid, rid)
}

func memberIDOf(m *sqlite.ChatGroupMemberRecord) string {
	if m == nil {
		return ""
	}
	return m.ID
}
