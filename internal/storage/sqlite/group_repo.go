package sqlite

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrGroupNotFound        = errors.New("group not found")
	ErrGroupVersionConflict = errors.New("group version conflict")
	ErrGroupMemberNotFound  = errors.New("group member not found")
	ErrGroupMemberConflict  = errors.New("group member already active")
	ErrGroupMessageNotFound = errors.New("group message not found")
)

type GroupRepo struct{ db *gorm.DB }

func NewGroupRepo(db *gorm.DB) *GroupRepo { return &GroupRepo{db: db} }

func groupUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Tx runs fn in a transaction with a repo bound to it.
// DB exposes the handle so sibling repositories (RequirementRepo, P6) can
// share this repository's transactions — the requirement pipeline writes
// requirements, jobs, messages and events in ONE transaction (WIRING §1.2
// of the P6 plan).
func (r *GroupRepo) DB() *gorm.DB { return r.db }

func (r *GroupRepo) Tx(ctx context.Context, fn func(tx *GroupRepo) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&GroupRepo{db: tx})
	})
}

// ---------- groups ----------

func (r *GroupRepo) CreateGroup(ctx context.Context, g *ChatGroupRecord) error {
	err := r.db.WithContext(ctx).Create(g).Error
	if groupUniqueViolation(err) {
		return ErrGroupVersionConflict
	}
	return err
}

func (r *GroupRepo) GetGroup(ctx context.Context, id string) (*ChatGroupRecord, error) {
	var g ChatGroupRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// UpdateGroupCAS applies fields only if the stored version equals version.
func (r *GroupRepo) UpdateGroupCAS(ctx context.Context, id string, version int64, fields map[string]any, at time.Time) error {
	fields["version"] = gorm.Expr("version + 1")
	fields["updated_at"] = at
	res := r.db.WithContext(ctx).Model(&ChatGroupRecord{}).Where("id = ? AND version = ?", id, version).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := r.GetGroup(ctx, id); err != nil {
			return err
		}
		return ErrGroupVersionConflict
	}
	return nil
}

type GroupCursor struct {
	UpdatedAt time.Time
	ID        string
}

type GroupListFilter struct {
	UserID string
	All    bool
	Status string
	Q      string
	After  *GroupCursor
	Limit  int
}

// ListGroups returns up to Limit groups (newest activity first) and whether more exist.
func (r *GroupRepo) ListGroups(ctx context.Context, f GroupListFilter) ([]ChatGroupRecord, bool, error) {
	q := r.db.WithContext(ctx).Model(&ChatGroupRecord{})
	if !f.All {
		sub := r.db.WithContext(ctx).Model(&ChatGroupMemberRecord{}).Select("group_id").
			Where("user_id = ? AND member_type = ? AND removed_at IS NULL", f.UserID, "user")
		q = q.Where("id IN (?)", sub)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Q != "" {
		q = q.Where(`name LIKE ? ESCAPE '\'`, "%"+escapeLike(f.Q)+"%")
	}
	if f.After != nil {
		q = q.Where("(updated_at < ? OR (updated_at = ? AND id < ?))", f.After.UpdatedAt, f.After.UpdatedAt, f.After.ID)
	}
	var out []ChatGroupRecord
	if err := q.Order("updated_at DESC, id DESC").Limit(f.Limit + 1).Find(&out).Error; err != nil {
		return nil, false, err
	}
	more := len(out) > f.Limit
	if more {
		out = out[:f.Limit]
	}
	return out, more, nil
}

// NextSeqs increments the group's event sequence (and the message sequence when
// msg is true) and returns the new values. Call it inside Tx.
func (r *GroupRepo) NextSeqs(ctx context.Context, groupID string, msg bool, at time.Time) (int64, int64, error) {
	fields := map[string]any{"event_seq": gorm.Expr("event_seq + 1"), "updated_at": at}
	if msg {
		fields["message_seq"] = gorm.Expr("message_seq + 1")
	}
	res := r.db.WithContext(ctx).Model(&ChatGroupRecord{}).Where("id = ?", groupID).Updates(fields)
	if res.Error != nil {
		return 0, 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, 0, ErrGroupNotFound
	}
	var row struct {
		MessageSeq int64
		EventSeq   int64
	}
	if err := r.db.WithContext(ctx).Model(&ChatGroupRecord{}).Select("message_seq, event_seq").
		Where("id = ?", groupID).Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	return row.MessageSeq, row.EventSeq, nil
}

// ---------- events ----------

func (r *GroupRepo) AppendEvent(ctx context.Context, e *GroupEventRecord) error {
	return r.db.WithContext(ctx).Create(e).Error
}

func (r *GroupRepo) ListEventsAfter(ctx context.Context, groupID string, after int64, limit int) ([]GroupEventRecord, error) {
	var out []GroupEventRecord
	err := r.db.WithContext(ctx).Where("group_id = ? AND seq > ?", groupID, after).
		Order("seq ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ---------- members ----------

func (r *GroupRepo) AddMember(ctx context.Context, m *ChatGroupMemberRecord) error {
	err := r.db.WithContext(ctx).Create(m).Error
	if groupUniqueViolation(err) {
		return ErrGroupMemberConflict
	}
	return err
}

func (r *GroupRepo) GetMember(ctx context.Context, groupID, memberID string) (*ChatGroupMemberRecord, error) {
	var m ChatGroupMemberRecord
	err := r.db.WithContext(ctx).Where("group_id = ? AND id = ?", groupID, memberID).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// FindActiveUserMember returns (nil, nil) when the user is not an active member.
func (r *GroupRepo) FindActiveUserMember(ctx context.Context, groupID, userID string) (*ChatGroupMemberRecord, error) {
	var m ChatGroupMemberRecord
	err := r.db.WithContext(ctx).
		Where("group_id = ? AND member_type = ? AND user_id = ? AND removed_at IS NULL", groupID, "user", userID).
		Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *GroupRepo) ListMembers(ctx context.Context, groupID string, includeRemoved bool) ([]ChatGroupMemberRecord, error) {
	q := r.db.WithContext(ctx).Where("group_id = ?", groupID)
	if !includeRemoved {
		q = q.Where("removed_at IS NULL")
	}
	var out []ChatGroupMemberRecord
	err := q.Order("added_at ASC, id ASC").Find(&out).Error
	return out, err
}

func (r *GroupRepo) UpdateMember(ctx context.Context, groupID, memberID string, fields map[string]any) error {
	res := r.db.WithContext(ctx).Model(&ChatGroupMemberRecord{}).
		Where("group_id = ? AND id = ? AND removed_at IS NULL", groupID, memberID).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrGroupMemberNotFound
	}
	return nil
}

func (r *GroupRepo) RemoveMember(ctx context.Context, groupID, memberID string, at time.Time) error {
	return r.UpdateMember(ctx, groupID, memberID, map[string]any{
		"removed_at": at,
		"active_key": gorm.Expr("NULL"),
	})
}

func (r *GroupRepo) CountActiveOwners(ctx context.Context, groupID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&ChatGroupMemberRecord{}).
		Where("group_id = ? AND group_role = ? AND removed_at IS NULL", groupID, "owner").Count(&n).Error
	return n, err
}

func (r *GroupRepo) CountActiveMembers(ctx context.Context, groupID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&ChatGroupMemberRecord{}).
		Where("group_id = ? AND removed_at IS NULL", groupID).Count(&n).Error
	return n, err
}

// ---------- messages ----------

// FindMessageByIdem returns (nil, nil) when no message has this idempotency key.
func (r *GroupRepo) FindMessageByIdem(ctx context.Context, key string) (*ChatMessageRecord, error) {
	var m ChatMessageRecord
	err := r.db.WithContext(ctx).Where("idem_key = ?", key).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *GroupRepo) CreateMessage(ctx context.Context, m *ChatMessageRecord) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *GroupRepo) GetMessage(ctx context.Context, groupID, id string) (*ChatMessageRecord, error) {
	var m ChatMessageRecord
	err := r.db.WithContext(ctx).Where("group_id = ? AND id = ?", groupID, id).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *GroupRepo) UpdateMessageBody(ctx context.Context, id, body string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&ChatMessageRecord{}).Where("id = ?", id).
		Updates(map[string]any{"body_md": body, "edited_at": at}).Error
}

func (r *GroupRepo) CreateMessageEdit(ctx context.Context, e *ChatMessageEditRecord) error {
	return r.db.WithContext(ctx).Create(e).Error
}

func (r *GroupRepo) CreateMentions(ctx context.Context, ms []ChatMentionRecord) error {
	if len(ms) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&ms).Error
}

func (r *GroupRepo) DeleteMentions(ctx context.Context, messageID string) error {
	return r.db.WithContext(ctx).Where("message_id = ?", messageID).Delete(&ChatMentionRecord{}).Error
}

func (r *GroupRepo) ListMentionsForMessages(ctx context.Context, ids []string) (map[string][]ChatMentionRecord, error) {
	out := map[string][]ChatMentionRecord{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []ChatMentionRecord
	if err := r.db.WithContext(ctx).Where("message_id IN ?", ids).Order("start_offset ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, m := range rows {
		out[m.MessageID] = append(out[m.MessageID], m)
	}
	return out, nil
}

type MessageListFilter struct {
	GroupID       string
	AfterSeq      int64
	BeforeSeq     int64
	RequirementID string
	Limit         int
}

// ListMessages returns messages in ascending seq order plus whether more exist in
// the paging direction. With AfterSeq it pages forward; otherwise it returns the
// newest messages before BeforeSeq (or the newest overall) and pages backward.
func (r *GroupRepo) ListMessages(ctx context.Context, f MessageListFilter) ([]ChatMessageRecord, bool, error) {
	q := r.db.WithContext(ctx).Where("group_id = ?", f.GroupID)
	if f.RequirementID != "" {
		q = q.Where("requirement_id = ?", f.RequirementID)
	}
	var out []ChatMessageRecord
	if f.AfterSeq > 0 {
		if err := q.Where("seq > ?", f.AfterSeq).Order("seq ASC").Limit(f.Limit + 1).Find(&out).Error; err != nil {
			return nil, false, err
		}
		more := len(out) > f.Limit
		if more {
			out = out[:f.Limit]
		}
		return out, more, nil
	}
	if f.BeforeSeq > 0 {
		q = q.Where("seq < ?", f.BeforeSeq)
	}
	if err := q.Order("seq DESC").Limit(f.Limit + 1).Find(&out).Error; err != nil {
		return nil, false, err
	}
	more := len(out) > f.Limit
	if more {
		out = out[:f.Limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, more, nil
}
