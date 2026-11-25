package sqlite

import "time"

// ChatGroupRecord is a group-chat shared space. Its SharedWorkspaceID points to
// a WorkspaceRecord with Kind=group_shared, which never has a runner.
type ChatGroupRecord struct {
	ID                string    `gorm:"primaryKey;size:40"`
	Name              string    `gorm:"size:120;not null"`
	Description       string    `gorm:"type:text;not null;default:''"`
	OwnerID           string    `gorm:"size:64;not null;index"`
	SharedWorkspaceID string    `gorm:"size:64;not null;uniqueIndex:uq_chat_groups_ws"`
	Policy            []byte    `gorm:"type:blob"`
	Budget            []byte    `gorm:"type:blob"`
	Status            string    `gorm:"size:16;not null;index"` // active|archived
	MessageSeq        int64     `gorm:"not null;default:0"`
	EventSeq          int64     `gorm:"not null;default:0"`
	Version           int64     `gorm:"not null;default:1"`
	CreatedAt         time.Time `gorm:"not null"`
	UpdatedAt         time.Time `gorm:"not null;index"`
}

func (ChatGroupRecord) TableName() string { return "chat_groups" }

// ChatGroupMemberRecord is a human or AI member. ActiveKey is non-NULL only while
// the member is active, so its unique index allows re-adding after removal
// (SQLite allows many NULLs in a unique index).
type ChatGroupMemberRecord struct {
	ID             string     `gorm:"primaryKey;size:40"`
	GroupID        string     `gorm:"size:40;not null;index"`
	MemberType     string     `gorm:"size:8;not null"` // user|ai
	UserID         string     `gorm:"size:64;not null;default:'';index"`
	AIModelID      string     `gorm:"size:40;not null;default:'';index"`
	GroupRole      string     `gorm:"size:16;not null"` // owner|operator|viewer|ai
	ParamsOverride []byte     `gorm:"type:blob"`
	AddedBy        string     `gorm:"size:64;not null"`
	AddedAt        time.Time  `gorm:"not null"`
	RemovedAt      *time.Time `gorm:"index"`
	ActiveKey      *string    `gorm:"size:160;uniqueIndex:uq_chat_group_members_active"`
}

func (ChatGroupMemberRecord) TableName() string { return "chat_group_members" }

type ChatMessageRecord struct {
	ID                string     `gorm:"primaryKey;size:40"`
	GroupID           string     `gorm:"size:40;not null;uniqueIndex:uq_chat_messages_group_seq,priority:1"`
	Seq               int64      `gorm:"not null;uniqueIndex:uq_chat_messages_group_seq,priority:2"`
	AuthorType        string     `gorm:"size:8;not null"` // user|ai|system
	AuthorMemberID    string     `gorm:"size:40;not null;default:''"`
	Kind              string     `gorm:"size:24;not null"` // text (P5); card kinds from P6
	BodyMD            string     `gorm:"type:text;not null;default:''"`
	Payload           []byte     `gorm:"type:blob"`
	RequirementID     string     `gorm:"size:40;not null;default:'';index"`
	ReplyToMessageID  string     `gorm:"size:40;not null;default:''"`
	ModelInvocationID string     `gorm:"size:40;not null;default:''"`
	ClientMsgID       string     `gorm:"size:64;not null;default:''"`
	IdemKey           *string    `gorm:"size:200;uniqueIndex:uq_chat_messages_idem"`
	Status            string     `gorm:"size:16;not null;default:'final'"`
	CreatedAt         time.Time  `gorm:"not null"`
	EditedAt          *time.Time
}

func (ChatMessageRecord) TableName() string { return "chat_messages" }

type ChatMentionRecord struct {
	ID             string `gorm:"primaryKey;size:40"`
	MessageID      string `gorm:"size:40;not null;index"`
	GroupID        string `gorm:"size:40;not null;index"`
	RawToken       string `gorm:"size:40;not null"`
	Handle         string `gorm:"size:32;not null"`
	TargetMemberID string `gorm:"size:40;not null;default:''"`
	Resolution     string `gorm:"size:20;not null"` // resolved|not_member|unknown|ignored_ai_author
	Intent         string `gorm:"size:16;not null"` // reference (P5)
	StartOffset    int    `gorm:"not null"`        // UTF-16 code units (JS string index)
	EndOffset      int    `gorm:"not null"`
}

func (ChatMentionRecord) TableName() string { return "chat_mentions" }

// ChatMessageEditRecord keeps the previous body on every edit (audit requirement).
type ChatMessageEditRecord struct {
	ID           string    `gorm:"primaryKey;size:40"`
	MessageID    string    `gorm:"size:40;not null;index"`
	PreviousBody string    `gorm:"type:text;not null"`
	EditedBy     string    `gorm:"size:64;not null"`
	EditedAt     time.Time `gorm:"not null"`
}

func (ChatMessageEditRecord) TableName() string { return "chat_message_edits" }

// GroupEventRecord is the durable group event log that backs SSE replay.
type GroupEventRecord struct {
	GroupID   string    `gorm:"primaryKey;size:40"`
	Seq       int64     `gorm:"primaryKey;autoIncrement:false"`
	Type      string    `gorm:"size:48;not null"`
	Data      []byte    `gorm:"type:blob"`
	CreatedAt time.Time `gorm:"not null"`
}

func (GroupEventRecord) TableName() string { return "group_events" }
