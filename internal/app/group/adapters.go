package group

import (
	"context"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/aicouncil/aicouncil/internal/app/aimodel"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// AIModelServiceDirectory adapts the landed P4 aimodel.Service.
// WIRING W7 (verified): Get(ctx,id) (*aimodel.View, error), List(ctx,
// sqlite.AIModelFilter) ([]aimodel.View, error), not-found as
// *aimodel.Error{Code:"not_found"}, View.Workspace is *aimodel.WorkspaceView
// with a Status field.
type AIModelServiceDirectory struct{ Svc *aimodel.Service }

func toInfo(v aimodel.View) AIModelInfo {
	info := AIModelInfo{ID: v.ID, Handle: v.Handle, DisplayName: v.DisplayName, Model: v.Model,
		ProviderName: v.ProviderName, ProviderProfileID: v.ProviderProfileID, Status: v.Status}
	if v.Workspace != nil {
		info.WorkspaceStatus = v.Workspace.Status
	}
	return info
}

func (d AIModelServiceDirectory) GetAIModel(ctx context.Context, id string) (AIModelInfo, error) {
	v, err := d.Svc.Get(ctx, id)
	if err != nil {
		var ae *aimodel.Error
		if errors.As(err, &ae) && ae.Code == "not_found" {
			return AIModelInfo{}, ErrDirectoryNotFound
		}
		return AIModelInfo{}, err
	}
	return toInfo(*v), nil
}

func (d AIModelServiceDirectory) FindAIModelByHandle(ctx context.Context, handle string) (AIModelInfo, error) {
	all, err := d.Svc.List(ctx, sqlite.AIModelFilter{})
	if err != nil {
		return AIModelInfo{}, err
	}
	for _, v := range all {
		if v.Handle == handle {
			return toInfo(v), nil
		}
	}
	return AIModelInfo{}, ErrDirectoryNotFound
}

func (d AIModelServiceDirectory) SearchAIModels(ctx context.Context, prefix string, limit int) ([]AIModelInfo, error) {
	all, err := d.Svc.List(ctx, sqlite.AIModelFilter{Status: "active"})
	if err != nil {
		return nil, err
	}
	out := []AIModelInfo{}
	for _, v := range all {
		if prefix == "" || strings.HasPrefix(v.Handle, prefix) || strings.HasPrefix(strings.ToLower(v.DisplayName), prefix) {
			out = append(out, toInfo(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GormUserDirectory reads users directly.
// WIRING W8 (adapted): sqlite.UserRecord has no Username/DisplayName — the
// account Subject is the human-facing identifier in this codebase, so it
// serves as both the mention handle source and the display name. GetUser
// resolves the canonical record ID first and falls back to a
// case-insensitive Subject match, so callers may pass either.
type GormUserDirectory struct{ DB *gorm.DB }

func userInfo(u sqlite.UserRecord) UserInfo {
	return UserInfo{ID: u.ID, Username: u.Subject, DisplayName: u.Subject}
}

func (d GormUserDirectory) GetUser(ctx context.Context, id string) (UserInfo, error) {
	var u sqlite.UserRecord
	err := d.DB.WithContext(ctx).Where("id = ?", id).Take(&u).Error
	if err == nil {
		return userInfo(u), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return UserInfo{}, err
	}
	err = d.DB.WithContext(ctx).Where("LOWER(subject) = ?", strings.ToLower(id)).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UserInfo{}, ErrDirectoryNotFound
	}
	if err != nil {
		return UserInfo{}, err
	}
	return userInfo(u), nil
}

func (d GormUserDirectory) FindUserByUsername(ctx context.Context, username string) (UserInfo, error) {
	var u sqlite.UserRecord
	err := d.DB.WithContext(ctx).Where("LOWER(subject) = ?", strings.ToLower(username)).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UserInfo{}, ErrDirectoryNotFound
	}
	if err != nil {
		return UserInfo{}, err
	}
	return userInfo(u), nil
}
