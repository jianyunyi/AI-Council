package council

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/app/wskind"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// InstallGroups constructs the Part B P5 group-chat service over the API's
// persistent store and installs it. It mirrors InstallAIModels: a nil store
// (in-memory-only API) leaves the group endpoints unregistered. Call after
// InstallAIModels — the AI member directory adapts the model registry.
func InstallGroups(a *API) *group.Service {
	if a.db == nil || a.aiModels == nil {
		return nil
	}
	svc := group.NewService(
		sqlite.NewGroupRepo(a.db),
		sqlite.NewRequirementRepo(a.db),
		&groupWorkspaceAdapter{a: a},
		group.AIModelServiceDirectory{Svc: a.aiModels},
		groupProviderAdapter{a: a},
		group.GormUserDirectory{DB: a.db},
		groupAuditAdapter{db: a.db},
		group.NewHub(256),
	)
	a.WithGroupService(svc)
	return svc
}

// groupWorkspaceAdapter implements group.WorkspaceCreator over the existing
// workspace registry (WIRING W6 — there is no CreateWorkspaceOfKind; this
// mirrors aimodel_wiring's aiModelWorkspaceAdapter). The group_shared kind
// is persisted and shown in the workspace list, but no runner is ever
// registered for it, and the startup runner migration already skips
// group_shared workspaces (cmd/council-server/main.go).
type groupWorkspaceAdapter struct{ a *API }

func (w *groupWorkspaceAdapter) CreateGroupWorkspace(_ context.Context, _ string, ownerID string) (string, error) {
	a := w.a
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	id := "workspace-" + strconv.FormatInt(a.nextID, 10)
	runnerPath := "groups/" + id
	if a.db != nil {
		record := sqlite.WorkspaceRecord{
			ID: id, OwnerUserID: ownerID, RunnerPath: runnerPath, Kind: wskind.GroupShared,
		}
		if err := a.db.Create(&record).Error; err != nil {
			a.nextID--
			return "", fmt.Errorf("create group workspace record: %w", err)
		}
	}
	a.workspaces[id] = workspace{ID: id, Kind: wskind.GroupShared, OwnerUserID: ownerID, RunnerPath: runnerPath}
	return id, nil
}

// ArchiveWorkspace marks the group workspace archived and drops it from the
// in-memory registry, mirroring aiModelWorkspaceAdapter.
func (w *groupWorkspaceAdapter) ArchiveWorkspace(_ context.Context, id string) error {
	a := w.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		if err := a.db.Model(&sqlite.WorkspaceRecord{}).
			Where("id = ?", id).
			Update("archived", true).Error; err != nil {
			return fmt.Errorf("archive group workspace record: %w", err)
		}
	}
	delete(a.workspaces, id)
	return nil
}

// groupAuditAdapter implements group.AuditSink over the existing audit
// table (WIRING W9 — there is no generic audit store; AuditRecord is
// run-scoped). Group entries are namespaced under RunID "group:<id>" and
// the per-run sequence is allocated inside a transaction so the
// (run_id, sequence) unique index holds.
type groupAuditAdapter struct{ db *gorm.DB }

func (ad groupAuditAdapter) Record(ctx context.Context, e group.AuditEntry) error {
	if ad.db == nil {
		return nil
	}
	detail := map[string]any{}
	for k, v := range e.Data {
		detail[k] = v
	}
	if e.TargetType != "" {
		detail["target_type"] = e.TargetType
	}
	if e.TargetID != "" {
		detail["target_id"] = e.TargetID
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	runID := "group:" + e.GroupID
	for attempt := 0; attempt < 5; attempt++ {
		err = ad.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var maxSeq int64
			if err := tx.Model(&sqlite.AuditRecord{}).
				Where("run_id = ?", runID).
				Select("COALESCE(MAX(sequence), 0)").
				Scan(&maxSeq).Error; err != nil {
				return err
			}
			return tx.Create(&sqlite.AuditRecord{
				ID:        "audit-" + hex.EncodeToString(random[:]),
				RunID:     runID,
				Sequence:  uint64(maxSeq) + 1,
				Type:      e.Action,
				Actor:     e.ActorID,
				Detail:    string(raw),
				CreatedAt: time.Now().UTC(),
			}).Error
		})
		if err == nil {
			return nil
		}
	}
	return err
}
