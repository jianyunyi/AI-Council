package group

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

const (
	MemberUser = "user"
	MemberAI   = "ai"

	RoleOwner    = "owner"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
	RoleAI       = "ai"

	StatusActive   = "active"
	StatusArchived = "archived"

	AuthorUser   = "user"
	AuthorAI     = "ai"
	AuthorSystem = "system"

	KindText = "text"

	MentionResolved        = "resolved"
	MentionNotMember       = "not_member"
	MentionUnknown         = "unknown"
	MentionIgnoredAIAuthor = "ignored_ai_author"

	IntentReference = "reference"

	EditWindow = 10 * time.Minute
)

type Service struct {
	repo     *sqlite.GroupRepo
	reqRepo  *sqlite.RequirementRepo // P6; nil disables dispatch
	ws       WorkspaceProvisioner
	models   AIModelDirectory
	provider ProviderInvoker // P6; nil disables dispatch
	users    UserDirectory
	audit    AuditSink
	hub      *Hub
	now      func() time.Time
	locks    sync.Map // groupID -> *sync.Mutex

	// P8 ports; all nil until EnablePushdown (P7 behavior otherwise).
	redTeamSeats RedTeamSeatSource
	tasks        TaskBridge
	modelWS      ModelWorkspaceLookup
	mirrors      *mirrorRegistry
}

func NewService(repo *sqlite.GroupRepo, reqRepo *sqlite.RequirementRepo, ws WorkspaceProvisioner, models AIModelDirectory, provider ProviderInvoker, users UserDirectory, audit AuditSink, hub *Hub) *Service {
	if hub == nil {
		hub = NewHub(256)
	}
	return &Service{repo: repo, reqRepo: reqRepo, ws: ws, models: models, provider: provider, users: users, audit: audit, hub: hub,
		now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Hub() *Hub { return s.hub }

func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func strPtr(s string) *string { return &s }

func (s *Service) lock(gid string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(gid, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func buildEvent(gid string, seq int64, typ string, data any, at time.Time) (*sqlite.GroupEventRecord, Event, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, Event{}, err
	}
	return &sqlite.GroupEventRecord{GroupID: gid, Seq: seq, Type: typ, Data: b, CreatedAt: at},
		Event{Seq: seq, Type: typ, At: at, Data: b}, nil
}

// mutate runs fn in a transaction, appends one event carrying fn's result, and
// publishes it while still holding the group lock, so live order equals seq order.
func (s *Service) mutate(ctx context.Context, gid, typ string, fn func(tx *sqlite.GroupRepo, at time.Time) (any, error)) (any, error) {
	mu := s.lock(gid)
	mu.Lock()
	defer mu.Unlock()
	at := s.now()
	var out any
	var ev Event
	err := s.repo.Tx(ctx, func(tx *sqlite.GroupRepo) error {
		data, err := fn(tx, at)
		if err != nil {
			return err
		}
		out = data
		_, evSeq, err := tx.NextSeqs(ctx, gid, false, at)
		if err != nil {
			return err
		}
		rec, e, err := buildEvent(gid, evSeq, typ, data, at)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, rec); err != nil {
			return err
		}
		ev = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.hub.Publish(gid, ev)
	return out, nil
}

func (s *Service) record(ctx context.Context, actorID, action, gid, targetType, targetID string, data map[string]any) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, AuditEntry{ActorID: actorID, Action: action, GroupID: gid,
		TargetType: targetType, TargetID: targetID, Data: data, At: s.now()}); err != nil {
		log.Printf("group: audit %s %s: %v", action, targetID, err)
	}
}

func mapRepoErr(err error) error {
	switch {
	case errors.Is(err, sqlite.ErrGroupNotFound):
		return errNotFound
	case errors.Is(err, sqlite.ErrGroupVersionConflict):
		return errVersionConflict
	case errors.Is(err, sqlite.ErrGroupMemberNotFound):
		return errMemberNotFound
	case errors.Is(err, sqlite.ErrGroupMemberConflict):
		return errMemberConflict
	case errors.Is(err, sqlite.ErrGroupMessageNotFound):
		return errMessageNotFound
	}
	return err
}

// access loads the group and the caller's active membership. Callers who are
// neither members nor holders of read:any/manage:any get 404, never 403, so
// group IDs can't be probed.
func (s *Service) access(ctx context.Context, a Actor, gid string) (*sqlite.ChatGroupRecord, *sqlite.ChatGroupMemberRecord, error) {
	g, err := s.repo.GetGroup(ctx, gid)
	if err != nil {
		return nil, nil, mapRepoErr(err)
	}
	var m *sqlite.ChatGroupMemberRecord
	if a.UserID != "" {
		m, err = s.repo.FindActiveUserMember(ctx, gid, a.UserID)
		if err != nil {
			return nil, nil, err
		}
	}
	if m == nil && !a.ReadAny && !a.ManageAny {
		return nil, nil, errNotFound
	}
	return g, m, nil
}

func canManage(a Actor, m *sqlite.ChatGroupMemberRecord) bool {
	return a.ManageAny || (m != nil && m.GroupRole == RoleOwner)
}

// AuthorizeRead checks read access (used by the SSE handler).
func (s *Service) AuthorizeRead(ctx context.Context, a Actor, gid string) error {
	_, _, err := s.access(ctx, a, gid)
	return err
}

// ---------------- members: description ----------------

func (s *Service) describeMember(ctx context.Context, rec *sqlite.ChatGroupMemberRecord) (MemberView, error) {
	v := MemberView{ID: rec.ID, MemberType: rec.MemberType, UserID: rec.UserID, AIModelID: rec.AIModelID,
		GroupRole: rec.GroupRole, AddedBy: rec.AddedBy, AddedAt: rec.AddedAt, RemovedAt: rec.RemovedAt}
	if len(rec.ParamsOverride) > 0 {
		_ = json.Unmarshal(rec.ParamsOverride, &v.ParamsOverride)
	}
	switch rec.MemberType {
	case MemberUser:
		u, err := s.users.GetUser(ctx, rec.UserID)
		if err != nil && !errors.Is(err, ErrDirectoryNotFound) {
			return v, err
		}
		if err != nil {
			v.DisplayName = "(unknown user)"
			return v, nil
		}
		v.Handle = strings.ToLower(u.Username)
		v.Mentionable = handleRe.MatchString(v.Handle) && rec.RemovedAt == nil
		v.DisplayName = u.DisplayName
		if v.DisplayName == "" {
			v.DisplayName = u.Username
		}
	case MemberAI:
		m, err := s.models.GetAIModel(ctx, rec.AIModelID)
		if err != nil && !errors.Is(err, ErrDirectoryNotFound) {
			return v, err
		}
		if err != nil {
			v.DisplayName = "(unknown model)"
			return v, nil
		}
		v.Handle, v.DisplayName, v.AIModel, v.ProviderName = m.Handle, m.DisplayName, m.Model, m.ProviderName
		v.AIStatus, v.WorkspaceStatus = m.Status, m.WorkspaceStatus
		v.Mentionable = rec.RemovedAt == nil
	}
	return v, nil
}

func (s *Service) describeMembers(ctx context.Context, recs []sqlite.ChatGroupMemberRecord) ([]MemberView, error) {
	out := make([]MemberView, 0, len(recs))
	for i := range recs {
		v, err := s.describeMember(ctx, &recs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ---------------- groups ----------------

type CreateGroupInput struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Policy      map[string]any   `json:"policy"`
	Budget      map[string]any   `json:"budget"`
	Members     []AddMemberInput `json:"members"`
}

type UpdateGroupInput struct {
	Name            *string        `json:"name"`
	Description     *string        `json:"description"`
	Policy          map[string]any `json:"policy"`
	Budget          map[string]any `json:"budget"`
	ExpectedVersion int64          `json:"expected_version"`
}

type ListGroupsInput struct {
	Status string
	Q      string
	Cursor string
	Limit  int
}

func validateNameDesc(name, desc string) (string, string, error) {
	name = strings.TrimSpace(name)
	desc = strings.TrimSpace(desc)
	if n := utf8.RuneCountInString(name); n < 1 || n > 120 {
		return "", "", errInvalidName
	}
	if utf8.RuneCountInString(desc) > 2000 {
		return "", "", errInvalidDesc
	}
	return name, desc, nil
}

func (s *Service) CreateGroup(ctx context.Context, a Actor, in CreateGroupInput) (*GroupDetail, error) {
	if !a.CanCreateGroup || a.UserID == "" {
		return nil, errForbidden
	}
	name, desc, err := validateNameDesc(in.Name, in.Description)
	if err != nil {
		return nil, err
	}
	policy, err := applyPolicy(DefaultPolicy(), in.Policy)
	if err != nil {
		return nil, err
	}
	budget, err := applyBudget(Budget{}, in.Budget)
	if err != nil {
		return nil, err
	}
	gid := newID("grp_")
	var initial []*sqlite.ChatGroupMemberRecord
	for _, mi := range in.Members {
		if mi.MemberType == MemberUser && mi.UserID == a.UserID {
			continue // creator is added as owner below
		}
		rec, err := s.prepareMember(ctx, gid, mi, a.UserID)
		if err != nil {
			return nil, err
		}
		initial = append(initial, rec)
	}
	policyJSON, _ := json.Marshal(policy)
	budgetJSON, _ := json.Marshal(budget)

	wsID, err := s.ws.CreateGroupWorkspace(ctx, "group:"+name, a.UserID)
	if err != nil {
		return nil, err
	}
	_, err = s.mutate(ctx, gid, "group.created", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		g := &sqlite.ChatGroupRecord{ID: gid, Name: name, Description: desc, OwnerID: a.UserID,
			SharedWorkspaceID: wsID, Policy: policyJSON, Budget: budgetJSON, Status: StatusActive,
			Version: 1, CreatedAt: at, UpdatedAt: at}
		if err := tx.CreateGroup(ctx, g); err != nil {
			return nil, err
		}
		owner := &sqlite.ChatGroupMemberRecord{ID: newID("gm_"), GroupID: gid, MemberType: MemberUser,
			UserID: a.UserID, GroupRole: RoleOwner, AddedBy: a.UserID, AddedAt: at,
			ActiveKey: strPtr(gid + ":u:" + a.UserID)}
		if err := tx.AddMember(ctx, owner); err != nil {
			return nil, err
		}
		for _, m := range initial {
			m.AddedAt = at
			if err := tx.AddMember(ctx, m); err != nil {
				return nil, err
			}
		}
		return map[string]any{"group_id": gid, "name": name}, nil
	})
	if err != nil {
		if aerr := s.ws.ArchiveWorkspace(context.Background(), wsID); aerr != nil {
			log.Printf("group: archive orphan workspace %s: %v", wsID, aerr)
		}
		return nil, mapRepoErr(err)
	}
	s.record(ctx, a.UserID, "group.created", gid, "group", gid, map[string]any{
		"name": name, "shared_workspace_id": wsID, "initial_members": len(initial)})
	return s.GetGroup(ctx, a, gid)
}

func (s *Service) groupView(ctx context.Context, a Actor, g *sqlite.ChatGroupRecord, m *sqlite.ChatGroupMemberRecord) (GroupView, error) {
	count, err := s.repo.CountActiveMembers(ctx, g.ID)
	if err != nil {
		return GroupView{}, err
	}
	v := GroupView{ID: g.ID, Name: g.Name, Description: g.Description, OwnerID: g.OwnerID,
		SharedWorkspaceID: g.SharedWorkspaceID, Policy: decodePolicy(g.Policy), Budget: decodeBudget(g.Budget),
		Status: g.Status, Version: g.Version, MemberCount: count, LastMessageSeq: g.MessageSeq,
		LastEventSeq: g.EventSeq, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, CanManage: canManage(a, m)}
	if m != nil {
		v.MyRole, v.MyMemberID = m.GroupRole, m.ID
	}
	return v, nil
}

func (s *Service) GetGroup(ctx context.Context, a Actor, gid string) (*GroupDetail, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	gv, err := s.groupView(ctx, a, g, m)
	if err != nil {
		return nil, err
	}
	recs, err := s.repo.ListMembers(ctx, gid, false)
	if err != nil {
		return nil, err
	}
	members, err := s.describeMembers(ctx, recs)
	if err != nil {
		return nil, err
	}
	return &GroupDetail{GroupView: gv, Members: members}, nil
}

type groupCursor struct {
	U  string `json:"u"`
	ID string `json:"id"`
}

func (s *Service) ListGroups(ctx context.Context, a Actor, in ListGroupsInput) ([]GroupView, string, error) {
	if a.UserID == "" && !a.ReadAny {
		return nil, "", errForbidden
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}
	if in.Limit > 200 {
		in.Limit = 200
	}
	if in.Status != "" && in.Status != StatusActive && in.Status != StatusArchived {
		return nil, "", newErr(400, "invalid_status", "status must be active|archived")
	}
	if utf8.RuneCountInString(in.Q) > 100 {
		return nil, "", newErr(400, "invalid_q", "q must be at most 100 characters")
	}
	f := sqlite.GroupListFilter{UserID: a.UserID, All: a.ReadAny, Status: in.Status, Q: in.Q, Limit: in.Limit}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil {
			return nil, "", errInvalidCursor
		}
		var c groupCursor
		if json.Unmarshal(raw, &c) != nil || c.ID == "" {
			return nil, "", errInvalidCursor
		}
		t, err := time.Parse(time.RFC3339Nano, c.U)
		if err != nil {
			return nil, "", errInvalidCursor
		}
		f.After = &sqlite.GroupCursor{UpdatedAt: t, ID: c.ID}
	}
	recs, more, err := s.repo.ListGroups(ctx, f)
	if err != nil {
		return nil, "", err
	}
	out := make([]GroupView, 0, len(recs))
	for i := range recs {
		m, err := s.repo.FindActiveUserMember(ctx, recs[i].ID, a.UserID)
		if err != nil {
			return nil, "", err
		}
		v, err := s.groupView(ctx, a, &recs[i], m)
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	next := ""
	if more && len(recs) > 0 {
		last := recs[len(recs)-1]
		b, _ := json.Marshal(groupCursor{U: last.UpdatedAt.UTC().Format(time.RFC3339Nano), ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, next, nil
}

func (s *Service) UpdateGroup(ctx context.Context, a Actor, gid string, in UpdateGroupInput) (*GroupDetail, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if !canManage(a, m) {
		return nil, errForbidden
	}
	if g.Status != StatusActive {
		return nil, errArchived
	}
	if in.ExpectedVersion <= 0 {
		return nil, errExpectedVersion
	}
	fields := map[string]any{}
	changed := []string{}
	if in.Name != nil || in.Description != nil {
		name, desc := g.Name, g.Description
		if in.Name != nil {
			name = *in.Name
		}
		if in.Description != nil {
			desc = *in.Description
		}
		name, desc, err = validateNameDesc(name, desc)
		if err != nil {
			return nil, err
		}
		fields["name"], fields["description"] = name, desc
		changed = append(changed, "name", "description")
	}
	if in.Policy != nil {
		p, err := applyPolicy(decodePolicy(g.Policy), in.Policy)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(p)
		fields["policy"] = b
		changed = append(changed, "policy")
	}
	if in.Budget != nil {
		bu, err := applyBudget(decodeBudget(g.Budget), in.Budget)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(bu)
		fields["budget"] = b
		changed = append(changed, "budget")
	}
	if len(fields) > 0 {
		_, err = s.mutate(ctx, gid, "group.updated", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
			if err := tx.UpdateGroupCAS(ctx, gid, in.ExpectedVersion, fields, at); err != nil {
				return nil, err
			}
			return map[string]any{"group_id": gid, "changed": changed, "version": in.ExpectedVersion + 1}, nil
		})
		if err != nil {
			return nil, mapRepoErr(err)
		}
		s.record(ctx, a.UserID, "group.updated", gid, "group", gid, map[string]any{"changed": changed})
	}
	return s.GetGroup(ctx, a, gid)
}

func (s *Service) ArchiveGroup(ctx context.Context, a Actor, gid string) (*GroupDetail, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if !canManage(a, m) {
		return nil, errForbidden
	}
	if g.Status == StatusArchived {
		return s.GetGroup(ctx, a, gid)
	}
	_, err = s.mutate(ctx, gid, "group.archived", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		if err := tx.UpdateGroupCAS(ctx, gid, g.Version, map[string]any{"status": StatusArchived}, at); err != nil {
			return nil, err
		}
		return map[string]any{"group_id": gid}, nil
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := s.ws.ArchiveWorkspace(ctx, g.SharedWorkspaceID); err != nil {
		log.Printf("group: archive workspace %s: %v", g.SharedWorkspaceID, err)
	}
	s.record(ctx, a.UserID, "group.archived", gid, "group", gid, nil)
	return s.GetGroup(ctx, a, gid)
}

// ---------------- members: mutations ----------------

type AddMemberInput struct {
	MemberType     string         `json:"member_type"`
	UserID         string         `json:"user_id"`
	AIModelID      string         `json:"ai_model_id"`
	GroupRole      string         `json:"group_role"`
	ParamsOverride map[string]any `json:"params_override"`
}

type UpdateMemberInput struct {
	GroupRole      *string        `json:"group_role"`
	ParamsOverride map[string]any `json:"params_override"`
}

func validUserRole(r string) bool { return r == RoleOwner || r == RoleOperator || r == RoleViewer }

func (s *Service) prepareMember(ctx context.Context, gid string, in AddMemberInput, addedBy string) (*sqlite.ChatGroupMemberRecord, error) {
	rec := &sqlite.ChatGroupMemberRecord{ID: newID("gm_"), GroupID: gid, MemberType: in.MemberType, AddedBy: addedBy, AddedAt: s.now()}

	switch in.MemberType {
	case MemberUser:
		if in.UserID == "" {
			return nil, errUserNotFound
		}
		// Adaptation (W8): the directory may resolve a subject/handle to the
		// canonical user record; membership rows must store the canonical ID
		// so access checks against the authenticated identity keep matching.
		u, err := s.users.GetUser(ctx, in.UserID)
		if err != nil {
			if errors.Is(err, ErrDirectoryNotFound) {
				return nil, errUserNotFound
			}
			return nil, err
		}
		role := in.GroupRole
		if role == "" {
			role = RoleOperator
		}
		if !validUserRole(role) {
			return nil, errInvalidRole
		}
		if len(in.ParamsOverride) > 0 {
			return nil, newErr(422, "params_not_allowed", "params_override applies to AI members only")
		}
		rec.UserID, rec.GroupRole = u.ID, role
		rec.ActiveKey = strPtr(gid + ":u:" + u.ID)
	case MemberAI:
		if in.AIModelID == "" {
			return nil, errAIModelNotFound
		}
		info, err := s.models.GetAIModel(ctx, in.AIModelID)
		if err != nil {
			if errors.Is(err, ErrDirectoryNotFound) {
				return nil, errAIModelNotFound
			}
			return nil, err
		}
		if info.Status != "active" {
			return nil, errAIModelNotActive
		}
		if in.GroupRole != "" && in.GroupRole != RoleAI {
			return nil, errAIRoleFixed
		}
		params, err := validateParamsOverride(in.ParamsOverride)
		if err != nil {
			return nil, err
		}
		rec.AIModelID, rec.GroupRole, rec.ParamsOverride = in.AIModelID, RoleAI, params
		rec.ActiveKey = strPtr(gid + ":a:" + in.AIModelID)
	default:
		return nil, errInvalidMemberType
	}
	return rec, nil
}

func (s *Service) ListMembers(ctx context.Context, a Actor, gid string, includeRemoved bool) ([]MemberView, error) {
	if _, _, err := s.access(ctx, a, gid); err != nil {
		return nil, err
	}
	recs, err := s.repo.ListMembers(ctx, gid, includeRemoved)
	if err != nil {
		return nil, err
	}
	return s.describeMembers(ctx, recs)
}

func (s *Service) AddMember(ctx context.Context, a Actor, gid string, in AddMemberInput) (*MemberView, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if !canManage(a, m) {
		return nil, errForbidden
	}
	if g.Status != StatusActive {
		return nil, errArchived
	}
	rec, err := s.prepareMember(ctx, gid, in, a.UserID)
	if err != nil {
		return nil, err
	}
	view, err := s.describeMember(ctx, rec)
	if err != nil {
		return nil, err
	}
	_, err = s.mutate(ctx, gid, "member.added", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		rec.AddedAt = at
		view.AddedAt = at
		if err := tx.AddMember(ctx, rec); err != nil {
			return nil, err
		}
		return view, nil
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	s.record(ctx, a.UserID, "group.member_added", gid, "group_member", rec.ID, map[string]any{
		"member_type": rec.MemberType, "user_id": rec.UserID, "ai_model_id": rec.AIModelID, "group_role": rec.GroupRole})
	return &view, nil
}

func (s *Service) UpdateMember(ctx context.Context, a Actor, gid, memberID string, in UpdateMemberInput) (*MemberView, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if !canManage(a, m) {
		return nil, errForbidden
	}
	if g.Status != StatusActive {
		return nil, errArchived
	}
	target, err := s.repo.GetMember(ctx, gid, memberID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if target.RemovedAt != nil {
		return nil, errMemberNotFound
	}
	fields := map[string]any{}
	if in.GroupRole != nil {
		if target.MemberType == MemberAI {
			if *in.GroupRole != RoleAI {
				return nil, errAIRoleFixed
			}
		} else {
			if !validUserRole(*in.GroupRole) {
				return nil, errInvalidRole
			}
			if *in.GroupRole == RoleOwner && !a.ManageAny && (m == nil || m.GroupRole != RoleOwner) {
				return nil, errForbidden
			}
			fields["group_role"] = *in.GroupRole
		}
	}
	if in.ParamsOverride != nil {
		if target.MemberType != MemberAI {
			return nil, newErr(422, "params_not_allowed", "params_override applies to AI members only")
		}
		b, err := validateParamsOverride(in.ParamsOverride)
		if err != nil {
			return nil, err
		}
		fields["params_override"] = b
	}
	if len(fields) == 0 {
		v, err := s.describeMember(ctx, target)
		return &v, err
	}
	demotingOwner := target.GroupRole == RoleOwner && fields["group_role"] != nil && fields["group_role"] != RoleOwner
	out, err := s.mutate(ctx, gid, "member.updated", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		if demotingOwner {
			n, err := tx.CountActiveOwners(ctx, gid)
			if err != nil {
				return nil, err
			}
			if n <= 1 {
				return nil, errLastOwner
			}
		}
		if err := tx.UpdateMember(ctx, gid, memberID, fields); err != nil {
			return nil, err
		}
		updated, err := tx.GetMember(ctx, gid, memberID)
		if err != nil {
			return nil, err
		}
		v, err := s.describeMember(ctx, updated)
		if err != nil {
			return nil, err
		}
		return v, nil
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	v := out.(MemberView)
	s.record(ctx, a.UserID, "group.member_updated", gid, "group_member", memberID, map[string]any{"fields": keys(fields)})
	return &v, nil
}

// RemoveMember is a soft remove. Owners/managers can remove anyone; a user can
// remove themselves (leave). The last owner can't be removed.
// P6 WIRING: block or prompt when the target is the assignee of an active requirement.
func (s *Service) RemoveMember(ctx context.Context, a Actor, gid, memberID string) error {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return err
	}
	target, err := s.repo.GetMember(ctx, gid, memberID)
	if err != nil {
		return mapRepoErr(err)
	}
	if target.RemovedAt != nil {
		return nil // idempotent
	}
	self := target.MemberType == MemberUser && target.UserID == a.UserID
	if !canManage(a, m) && !self {
		return errForbidden
	}
	if g.Status != StatusActive {
		return errArchived
	}
	_, err = s.mutate(ctx, gid, "member.removed", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		if target.GroupRole == RoleOwner {
			n, err := tx.CountActiveOwners(ctx, gid)
			if err != nil {
				return nil, err
			}
			if n <= 1 {
				return nil, errLastOwner
			}
		}
		if err := tx.RemoveMember(ctx, gid, memberID, at); err != nil {
			return nil, err
		}
		return map[string]any{"member_id": memberID, "member_type": target.MemberType,
			"user_id": target.UserID, "ai_model_id": target.AIModelID}, nil
	})
	if err != nil {
		return mapRepoErr(err)
	}
	if target.MemberType == MemberUser {
		s.hub.Kick(gid, target.UserID)
	}
	s.record(ctx, a.UserID, "group.member_removed", gid, "group_member", memberID, map[string]any{"self": self})
	return nil
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------- messages ----------------

type PostMessageInput struct {
	BodyMD           string `json:"body_md"`
	ClientMsgID      string `json:"client_msg_id"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	RequirementID    string `json:"requirement_id"`
	Intent           string `json:"intent"`
}

type PostMessageResult struct {
	Message   MessageView `json:"message"`
	Duplicate bool        `json:"duplicate"`
}

type ListMessagesInput struct {
	AfterSeq      int64
	BeforeSeq     int64
	RequirementID string
	Limit         int
}

type MessagePage struct {
	Items   []MessageView `json:"items"`
	HasMore bool          `json:"has_more"`
}

func checkIntent(intent string) error {
	switch intent {
	case "", "chat", "dispatch", "review":
		return nil
	case "result_review":
		return errIntentUnavailable // enabled in P8
	default:
		return errInvalidIntent
	}
}

// authorMap describes every member that has ever been in the group, keyed by ID.
func (s *Service) authorMap(ctx context.Context, gid string) (map[string]MemberView, []MemberView, error) {
	recs, err := s.repo.ListMembers(ctx, gid, true)
	if err != nil {
		return nil, nil, err
	}
	views, err := s.describeMembers(ctx, recs)
	if err != nil {
		return nil, nil, err
	}
	m := make(map[string]MemberView, len(views))
	for _, v := range views {
		m[v.ID] = v
	}
	return m, views, nil
}

func mentionableHandles(views []MemberView) map[string]string {
	out := map[string]string{}
	for _, v := range views {
		if v.RemovedAt == nil && v.Mentionable && v.Handle != "" {
			out[v.Handle] = v.ID
		}
	}
	return out
}

func (s *Service) resolveMentions(ctx context.Context, gid, messageID, body, authorType string, handles map[string]string) ([]sqlite.ChatMentionRecord, error) {
	tokens := ParseMentions(body)
	out := make([]sqlite.ChatMentionRecord, 0, len(tokens))
	cache := map[string]string{} // handle -> not_member|unknown
	for _, t := range tokens {
		rec := sqlite.ChatMentionRecord{ID: newID("mnt_"), MessageID: messageID, GroupID: gid, RawToken: t.Raw,
			Handle: t.Handle, Intent: IntentReference, StartOffset: t.Start, EndOffset: t.End}
		if id, ok := handles[t.Handle]; ok {
			rec.TargetMemberID, rec.Resolution = id, MentionResolved
		} else if r, ok := cache[t.Handle]; ok {
			rec.Resolution = r
		} else {
			r, err := s.classifyNonMember(ctx, t.Handle)
			if err != nil {
				return nil, err
			}
			cache[t.Handle] = r
			rec.Resolution = r
		}
		// AI-authored mentions never trigger anything (loop breaker). They are
		// still recorded, and the target is kept so the UI can highlight it.
		if authorType == AuthorAI {
			rec.Resolution = MentionIgnoredAIAuthor
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *Service) classifyNonMember(ctx context.Context, handle string) (string, error) {
	if _, err := s.models.FindAIModelByHandle(ctx, handle); err == nil {
		return MentionNotMember, nil
	} else if !errors.Is(err, ErrDirectoryNotFound) {
		return "", err
	}
	if _, err := s.users.FindUserByUsername(ctx, handle); err == nil {
		return MentionNotMember, nil
	} else if !errors.Is(err, ErrDirectoryNotFound) {
		return "", err
	}
	return MentionUnknown, nil
}

func (s *Service) PostMessage(ctx context.Context, a Actor, gid string, in PostMessageInput) (*PostMessageResult, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errViewerCannotPost
	}
	if g.Status != StatusActive {
		return nil, errArchived
	}
	if err := checkIntent(in.Intent); err != nil {
		return nil, err
	}
	// P7: only a review message may carry a requirement id, and a review
	// message must carry one. Chat/dispatch messages keep the P6 rule.
	if in.Intent == "review" {
		if in.RequirementID == "" {
			return nil, errRequirementRequired
		}
	} else if in.RequirementID != "" {
		return nil, errRequirementUnavail
	}
	body := strings.TrimRight(in.BodyMD, " \t\r\n")
	if strings.TrimSpace(body) == "" || !validBodyLen(body) {
		return nil, errInvalidBody
	}
	if n := utf8.RuneCountInString(in.ClientMsgID); n < 1 || n > 64 {
		return nil, errInvalidClientMsgID
	}
	if in.ReplyToMessageID != "" {
		if _, err := s.repo.GetMessage(ctx, gid, in.ReplyToMessageID); err != nil {
			if errors.Is(err, sqlite.ErrGroupMessageNotFound) {
				return nil, errReplyNotFound
			}
			return nil, err
		}
	}
	authors, views, err := s.authorMap(ctx, gid)
	if err != nil {
		return nil, err
	}
	msgID := newID("msg_")
	mentions, err := s.resolveMentions(ctx, gid, msgID, body, AuthorUser, mentionableHandles(views))
	if err != nil {
		return nil, err
	}
	// P6 dispatch: validate everything that can fail BEFORE the message is
	// written, so a rejected dispatch posts nothing. Only owners/operators
	// (or group:manage:any holders) may dispatch, and only to AI members
	// that are active with a ready workspace. PostMessage is human-only —
	// the author is the caller's own member row — so AI-authored dispatch
	// is inert by construction.
	var specs []dispatchTarget
	if in.Intent == "dispatch" {
		if s.provider == nil || s.reqRepo == nil {
			return nil, errIntentUnavailable
		}
		if m.GroupRole != RoleOwner && m.GroupRole != RoleOperator && !a.ManageAny {
			return nil, errDispatchForbidden
		}
		specs = extractCallSpecs(mentions, authors)
		if len(specs) == 0 {
			return nil, errNoCallMentions
		}
		for _, spec := range specs {
			if _, _, err := s.validateAssignee(ctx, spec.MemberID, authors); err != nil {
				return nil, err
			}
		}
	}
	// P7 review: same validate-before-write discipline as dispatch. The
	// mentioned members (humans and AIs) become the round's reviewers;
	// rvPrepareReview checks membership, roles, the assignee exclusion
	// and the policy's reviewer-count bounds.
	var reviewPlan *reviewStartPlan
	if in.Intent == "review" {
		if s.provider == nil || s.reqRepo == nil {
			return nil, errIntentUnavailable
		}
		if m.GroupRole != RoleOwner && m.GroupRole != RoleOperator && !a.ManageAny {
			return nil, errReviewForbidden
		}
		plan, err := s.rvPrepareReview(ctx, a, g, m, in.RequirementID, reviewerIDsFromMentions(mentions, m.ID))
		if err != nil {
			return nil, err
		}
		reviewPlan = plan
	}
	idem := gid + ":" + m.ID + ":" + in.ClientMsgID

	mu := s.lock(gid)
	mu.Lock()
	defer mu.Unlock()
	at := s.now()
	var view MessageView
	var ev Event
	var extra []Event
	var reqIDs []string
	reviewStarted := false
	dup := false
	err = s.repo.Tx(ctx, func(tx *sqlite.GroupRepo) error {
		existing, err := tx.FindMessageByIdem(ctx, idem)
		if err != nil {
			return err
		}
		if existing != nil {
			ms, err := tx.ListMentionsForMessages(ctx, []string{existing.ID})
			if err != nil {
				return err
			}
			view, dup = messageView(*existing, ms[existing.ID], authors), true
			return nil
		}
		msgSeq, evSeq, err := tx.NextSeqs(ctx, gid, true, at)
		if err != nil {
			return err
		}
		rec := &sqlite.ChatMessageRecord{ID: msgID, GroupID: gid, Seq: msgSeq, AuthorType: AuthorUser,
			AuthorMemberID: m.ID, Kind: KindText, BodyMD: body, ReplyToMessageID: in.ReplyToMessageID,
			ClientMsgID: in.ClientMsgID, IdemKey: strPtr(idem), Status: "final", CreatedAt: at}
		if reviewPlan != nil {
			rec.RequirementID = in.RequirementID // the review anchor message
		}
		if err := tx.CreateMessage(ctx, rec); err != nil {
			return err
		}
		if err := tx.CreateMentions(ctx, mentions); err != nil {
			return err
		}
		view = messageView(*rec, mentions, authors)
		evRec, e, err := buildEvent(gid, evSeq, "message.created", view, at)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, evRec); err != nil {
			return err
		}
		ev = e
		if len(specs) > 0 {
			ids, cardEvs, err := s.dispatchFanout(ctx, tx, g, m, msgID, body, specs, authors, at)
			if err != nil {
				return err
			}
			reqIDs, extra = ids, cardEvs
		}
		if reviewPlan != nil {
			evs, err := s.rvStartReviewTx(ctx, tx, g, a.UserID, m.ID, msgID, reviewPlan, at)
			if err != nil {
				return err
			}
			extra = append(extra, evs...)
			reviewStarted = true
		}
		return nil
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if !dup {
		s.hub.Publish(gid, ev)
		for _, e := range extra {
			s.hub.Publish(gid, e)
		}
		s.record(ctx, a.UserID, "group.message_created", gid, "chat_message", view.ID, map[string]any{
			"seq": view.Seq, "mentions": len(mentions)})
		for _, rid := range reqIDs {
			s.record(ctx, a.UserID, "group.requirement_dispatched", gid, "group_requirement", rid, map[string]any{
				"anchor_message_id": msgID})
		}
		if reviewStarted {
			s.record(ctx, a.UserID, "group.review_started", gid, "group_requirement", in.RequirementID, map[string]any{
				"round_id": reviewPlan.roundID, "via": "chat"})
		}
	}
	return &PostMessageResult{Message: view, Duplicate: dup}, nil
}

func (s *Service) EditMessage(ctx context.Context, a Actor, gid, messageID, newBody string) (*MessageView, error) {
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errViewerCannotPost
	}
	if g.Status != StatusActive {
		return nil, errArchived
	}
	msg, err := s.repo.GetMessage(ctx, gid, messageID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if msg.AuthorType != AuthorUser || msg.Kind != KindText || msg.AuthorMemberID != m.ID ||
		s.now().Sub(msg.CreatedAt) > EditWindow {
		return nil, errNotEditable
	}
	body := strings.TrimRight(newBody, " \t\r\n")
	if strings.TrimSpace(body) == "" || !validBodyLen(body) {
		return nil, errInvalidBody
	}
	authors, views, err := s.authorMap(ctx, gid)
	if err != nil {
		return nil, err
	}
	mentions, err := s.resolveMentions(ctx, gid, messageID, body, AuthorUser, mentionableHandles(views))
	if err != nil {
		return nil, err
	}
	out, err := s.mutate(ctx, gid, "message.updated", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		if err := tx.CreateMessageEdit(ctx, &sqlite.ChatMessageEditRecord{ID: newID("med_"), MessageID: messageID,
			PreviousBody: msg.BodyMD, EditedBy: a.UserID, EditedAt: at}); err != nil {
			return nil, err
		}
		if err := tx.UpdateMessageBody(ctx, messageID, body, at); err != nil {
			return nil, err
		}
		if err := tx.DeleteMentions(ctx, messageID); err != nil {
			return nil, err
		}
		if err := tx.CreateMentions(ctx, mentions); err != nil {
			return nil, err
		}
		updated := *msg
		updated.BodyMD = body
		updated.EditedAt = &at
		return messageView(updated, mentions, authors), nil
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	v := out.(MessageView)
	s.record(ctx, a.UserID, "group.message_edited", gid, "chat_message", messageID, nil)
	return &v, nil
}

func (s *Service) ListMessages(ctx context.Context, a Actor, gid string, in ListMessagesInput) (*MessagePage, error) {
	if _, _, err := s.access(ctx, a, gid); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}
	if in.Limit > 200 {
		in.Limit = 200
	}
	if in.AfterSeq < 0 || in.BeforeSeq < 0 || (in.AfterSeq > 0 && in.BeforeSeq > 0) {
		return nil, newErr(400, "invalid_range", "use either after_seq or before_seq")
	}
	recs, more, err := s.repo.ListMessages(ctx, sqlite.MessageListFilter{GroupID: gid, AfterSeq: in.AfterSeq,
		BeforeSeq: in.BeforeSeq, RequirementID: in.RequirementID, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	ms, err := s.repo.ListMentionsForMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	authors, _, err := s.authorMap(ctx, gid)
	if err != nil {
		return nil, err
	}
	page := &MessagePage{Items: make([]MessageView, 0, len(recs)), HasMore: more}
	for _, r := range recs {
		page.Items = append(page.Items, messageView(r, ms[r.ID], authors))
	}
	return page, nil
}

// ResolveMentionCandidates powers the @ picker. Active members match by handle or
// display-name prefix. Managers also see active non-member AI models
// (IsMember=false) so they can be added.
func (s *Service) ResolveMentionCandidates(ctx context.Context, a Actor, gid, q string) ([]MentionCandidate, error) {
	_, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q), "@"))
	recs, err := s.repo.ListMembers(ctx, gid, false)
	if err != nil {
		return nil, err
	}
	views, err := s.describeMembers(ctx, recs)
	if err != nil {
		return nil, err
	}
	const limit = 10
	out := []MentionCandidate{}
	memberModels := map[string]bool{}
	for _, v := range views {
		if v.MemberType == MemberAI {
			memberModels[v.AIModelID] = true
		}
		if !v.Mentionable {
			continue
		}
		if q == "" || strings.HasPrefix(v.Handle, q) || strings.HasPrefix(strings.ToLower(v.DisplayName), q) {
			st := v.AIStatus
			out = append(out, MentionCandidate{Handle: v.Handle, DisplayName: v.DisplayName, MemberType: v.MemberType,
				MemberID: v.ID, AIModelID: v.AIModelID, IsMember: true, Status: st})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MemberType != out[j].MemberType {
			return out[i].MemberType == MemberAI
		}
		return out[i].Handle < out[j].Handle
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if canManage(a, m) && len(out) < limit {
		extra, err := s.models.SearchAIModels(ctx, q, limit)
		if err != nil {
			return nil, err
		}
		for _, e := range extra {
			if memberModels[e.ID] || len(out) >= limit {
				continue
			}
			out = append(out, MentionCandidate{Handle: e.Handle, DisplayName: e.DisplayName, MemberType: MemberAI,
				AIModelID: e.ID, IsMember: false, Status: e.Status})
		}
	}
	return out, nil
}

// EventsAfter replays durable events with seq > after (SSE resume).
func (s *Service) EventsAfter(ctx context.Context, gid string, after int64, limit int) ([]Event, error) {
	recs, err := s.repo.ListEventsAfter(ctx, gid, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(recs))
	for i, r := range recs {
		out[i] = Event{Seq: r.Seq, Type: r.Type, At: r.CreatedAt, Data: json.RawMessage(r.Data)}
	}
	return out, nil
}
