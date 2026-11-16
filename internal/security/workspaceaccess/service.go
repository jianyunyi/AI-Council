package workspaceaccess

import (
	"context"
	"errors"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Access string

const (
	AccessNone Access = "none"
	Viewer     Access = "viewer"
	Editor     Access = "editor"
)

var (
	ErrWorkspaceNotFound  = errors.New("workspace not found")
	ErrForbidden          = errors.New("workspace owner permission required")
	ErrInvalidShare       = errors.New("invalid workspace share")
	ErrTargetUserNotFound = errors.New("active target user not found")
)

type Share struct {
	UserID    string    `json:"user_id"`
	Subject   string    `json:"subject"`
	Access    Access    `json:"access"`
	GrantedBy string    `json:"granted_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Member struct {
	Subject string `json:"subject"`
}

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) EffectiveAccess(ctx context.Context, workspaceID, userID string) (Access, error) {
	var workspace sqlite.WorkspaceRecord
	if err := s.db.WithContext(ctx).Where("id = ?", workspaceID).First(&workspace).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AccessNone, ErrWorkspaceNotFound
		}
		return AccessNone, err
	}
	if workspace.OwnerUserID == userID {
		return Editor, nil
	}
	if userID == "" {
		return AccessNone, nil
	}
	var share sqlite.WorkspaceShareRecord
	err := s.db.WithContext(ctx).Where("workspace_id = ? AND user_id = ?", workspaceID, userID).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AccessNone, nil
	}
	if err != nil {
		return AccessNone, err
	}
	return Access(share.Access), nil
}

func (s *Service) CanRead(ctx context.Context, workspaceID, userID string) (bool, error) {
	access, err := s.EffectiveAccess(ctx, workspaceID, userID)
	return access == Editor || access == Viewer, err
}

func (s *Service) CanEdit(ctx context.Context, workspaceID, userID string) (bool, error) {
	access, err := s.EffectiveAccess(ctx, workspaceID, userID)
	return access == Editor, err
}

func (s *Service) List(ctx context.Context, workspaceID, ownerID string) ([]Share, error) {
	canRead, err := s.CanRead(ctx, workspaceID, ownerID)
	if err != nil {
		return nil, err
	}
	if !canRead {
		if ownerID == "" {
			return nil, ErrForbidden
		}
		return nil, ErrForbidden
	}
	var records []sqlite.WorkspaceShareRecord
	if err := s.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).Order("created_at").Find(&records).Error; err != nil {
		return nil, err
	}
	shares := make([]Share, 0, len(records))
	for _, record := range records {
		var user sqlite.UserRecord
		if err := s.db.WithContext(ctx).Where("id = ?", record.UserID).First(&user).Error; err != nil {
			return nil, err
		}
		shares = append(shares, Share{UserID: user.ID, Subject: user.Subject, Access: Access(record.Access), GrantedBy: record.GrantedBy, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt})
	}
	return shares, nil
}

func (s *Service) Grant(ctx context.Context, workspaceID, ownerID, subject string, access Access) error {
	if access != Viewer && access != Editor {
		return ErrInvalidShare
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireOwner(tx, workspaceID, ownerID); err != nil {
			return err
		}
		var target sqlite.UserRecord
		if err := tx.Where("subject = ? AND disabled = ?", subject, false).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTargetUserNotFound
			}
			return err
		}
		if target.ID == ownerID {
			return ErrInvalidShare
		}
		now := time.Now().UTC()
		record := sqlite.WorkspaceShareRecord{WorkspaceID: workspaceID, UserID: target.ID, Access: string(access), GrantedBy: ownerID, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "workspace_id"}, {Name: "user_id"}}, DoUpdates: clause.Assignments(map[string]any{"access": string(access), "granted_by": ownerID, "updated_at": now})}).Create(&record).Error
	})
}

func (s *Service) Revoke(ctx context.Context, workspaceID, ownerID, subject string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireOwner(tx, workspaceID, ownerID); err != nil {
			return err
		}
		var target sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTargetUserNotFound
			}
			return err
		}
		return tx.Where("workspace_id = ? AND user_id = ?", workspaceID, target.ID).Delete(&sqlite.WorkspaceShareRecord{}).Error
	})
}

func (s *Service) SearchMembers(ctx context.Context, workspaceID, ownerID, prefix string) ([]Member, error) {
	if err := s.requireOwner(ctx, workspaceID, ownerID); err != nil {
		return nil, err
	}
	if len([]rune(prefix)) < 2 {
		return []Member{}, nil
	}
	var users []sqlite.UserRecord
	if err := s.db.WithContext(ctx).Where("disabled = ? AND subject LIKE ?", false, prefix+"%").Order("subject").Limit(20).Find(&users).Error; err != nil {
		return nil, err
	}
	result := make([]Member, 0, len(users))
	for _, user := range users {
		if user.ID != ownerID {
			result = append(result, Member{Subject: user.Subject})
		}
	}
	return result, nil
}

func (s *Service) requireOwner(ctx context.Context, workspaceID, ownerID string) error {
	return requireOwner(s.db.WithContext(ctx), workspaceID, ownerID)
}

func requireOwner(db *gorm.DB, workspaceID, ownerID string) error {
	var workspace sqlite.WorkspaceRecord
	if err := db.Where("id = ?", workspaceID).First(&workspace).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkspaceNotFound
		}
		return err
	}
	if ownerID == "" || workspace.OwnerUserID != ownerID {
		return ErrForbidden
	}
	return nil
}
