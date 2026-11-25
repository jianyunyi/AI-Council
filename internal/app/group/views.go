package group

import (
	"encoding/json"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

type MemberView struct {
	ID              string         `json:"id"`
	MemberType      string         `json:"member_type"`
	UserID          string         `json:"user_id,omitempty"`
	AIModelID       string         `json:"ai_model_id,omitempty"`
	GroupRole       string         `json:"group_role"`
	Handle          string         `json:"handle"`
	Mentionable     bool           `json:"mentionable"`
	DisplayName     string         `json:"display_name"`
	AIModel         string         `json:"ai_model,omitempty"`
	ProviderName    string         `json:"provider_name,omitempty"`
	AIStatus        string         `json:"ai_status,omitempty"`
	WorkspaceStatus string         `json:"workspace_status,omitempty"`
	ParamsOverride  map[string]any `json:"params_override,omitempty"`
	AddedBy         string         `json:"added_by"`
	AddedAt         time.Time      `json:"added_at"`
	RemovedAt       *time.Time     `json:"removed_at,omitempty"`
}

type GroupView struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	OwnerID           string    `json:"owner_id"`
	SharedWorkspaceID string    `json:"shared_workspace_id"`
	Policy            Policy    `json:"policy"`
	Budget            Budget    `json:"budget"`
	Status            string    `json:"status"`
	Version           int64     `json:"version"`
	MyRole            string    `json:"my_role"`
	MyMemberID        string    `json:"my_member_id,omitempty"`
	CanManage         bool      `json:"can_manage"`
	MemberCount       int64     `json:"member_count"`
	LastMessageSeq    int64     `json:"last_message_seq"`
	LastEventSeq      int64     `json:"last_event_seq"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type GroupDetail struct {
	GroupView
	Members []MemberView `json:"members"`
}

type AuthorView struct {
	MemberType  string `json:"member_type"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
}

type MentionView struct {
	RawToken       string `json:"raw_token"`
	Handle         string `json:"handle"`
	TargetMemberID string `json:"target_member_id,omitempty"`
	Resolution     string `json:"resolution"`
	Intent         string `json:"intent"`
	Start          int    `json:"start"`
	End            int    `json:"end"`
}

type MessageView struct {
	ID               string          `json:"id"`
	GroupID          string          `json:"group_id"`
	Seq              int64           `json:"seq"`
	AuthorType       string          `json:"author_type"`
	AuthorMemberID   string          `json:"author_member_id,omitempty"`
	Author           *AuthorView     `json:"author,omitempty"`
	Kind             string          `json:"kind"`
	BodyMD           string          `json:"body_md"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	RequirementID    string          `json:"requirement_id,omitempty"`
	ReplyToMessageID string          `json:"reply_to_message_id,omitempty"`
	ClientMsgID      string          `json:"client_msg_id,omitempty"`
	Status           string          `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
	EditedAt         *time.Time      `json:"edited_at,omitempty"`
	Mentions         []MentionView   `json:"mentions"`
}

type MentionCandidate struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	MemberType  string `json:"member_type"`
	MemberID    string `json:"member_id,omitempty"`
	AIModelID   string `json:"ai_model_id,omitempty"`
	IsMember    bool   `json:"is_member"`
	Status      string `json:"status,omitempty"`
}

func messageView(rec sqlite.ChatMessageRecord, mentions []sqlite.ChatMentionRecord, authors map[string]MemberView) MessageView {
	v := MessageView{
		ID: rec.ID, GroupID: rec.GroupID, Seq: rec.Seq, AuthorType: rec.AuthorType, AuthorMemberID: rec.AuthorMemberID,
		Kind: rec.Kind, BodyMD: rec.BodyMD, RequirementID: rec.RequirementID, ReplyToMessageID: rec.ReplyToMessageID,
		ClientMsgID: rec.ClientMsgID, Status: rec.Status, CreatedAt: rec.CreatedAt, EditedAt: rec.EditedAt,
		Mentions: make([]MentionView, 0, len(mentions)),
	}
	if len(rec.Payload) > 0 {
		v.Payload = json.RawMessage(rec.Payload)
	}
	if a, ok := authors[rec.AuthorMemberID]; ok {
		v.Author = &AuthorView{MemberType: a.MemberType, Handle: a.Handle, DisplayName: a.DisplayName}
	}
	for _, m := range mentions {
		v.Mentions = append(v.Mentions, MentionView{
			RawToken: m.RawToken, Handle: m.Handle, TargetMemberID: m.TargetMemberID,
			Resolution: m.Resolution, Intent: m.Intent, Start: m.StartOffset, End: m.EndOffset,
		})
	}
	return v
}
