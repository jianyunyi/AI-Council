package council

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/roles"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Council role assignment endpoints (design §A3). Assignments are stored per
// account: a global set plus an optional overriding set per workspace.
// Responses never include provider keys — only profile metadata.

type councilRolesScope struct {
	ScopeType string
	ScopeID   string
}

type councilRolesResponse struct {
	ScopeType       string         `json:"scope_type"`
	ScopeID         string         `json:"scope_id,omitempty"`
	Version         int            `json:"version"`
	EffectiveSource string         `json:"effective_source"`
	Roles           []roles.Slot   `json:"roles"`
	ResolvedPreview roles.Resolved `json:"resolved_preview"`
	Warnings        []roles.Issue  `json:"warnings"`
}

type councilRolesPutRequest struct {
	ScopeType string       `json:"scope_type"`
	ScopeID   string       `json:"scope_id"`
	Version   *int         `json:"version"`
	Slots     []roles.Slot `json:"slots"`
}

func (a *API) councilRolesReady(w http.ResponseWriter) bool {
	if a.councilRoles == nil || a.providerKeys == nil {
		writeErr(w, http.StatusServiceUnavailable, "council_roles_unavailable", "council role assignments require provider key storage")
		return false
	}
	return true
}

func (a *API) councilProfiles(ctx context.Context, ownerID string) ([]roles.ProfileInfo, error) {
	profiles, err := a.providerKeys.List(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]roles.ProfileInfo, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, roles.ProfileInfo{ID: profile.ID, Provider: profile.Provider, Model: profile.Model, HasKey: profile.HasKey})
	}
	return out, nil
}

func (a *API) councilResolver() *roles.Resolver {
	return roles.NewResolver(a.councilRoles, a.councilProfiles)
}

func parseCouncilScope(w http.ResponseWriter, scopeType, scopeID string) (councilRolesScope, bool) {
	scopeType = strings.TrimSpace(scopeType)
	if scopeType == "" {
		scopeType = roles.ScopeGlobal
	}
	if scopeType != roles.ScopeGlobal && scopeType != roles.ScopeWorkspace {
		writeErr(w, http.StatusBadRequest, "invalid_scope", "scope_type must be global or workspace")
		return councilRolesScope{}, false
	}
	if scopeType == roles.ScopeWorkspace && strings.TrimSpace(scopeID) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_scope", "scope_id is required for workspace scope")
		return councilRolesScope{}, false
	}
	if scopeType == roles.ScopeGlobal {
		scopeID = ""
	}
	return councilRolesScope{ScopeType: scopeType, ScopeID: scopeID}, true
}

func (a *API) hasCouncilPermission(r *http.Request, permission string) bool {
	identity, ok := IdentityFromRequest(r)
	if !ok {
		// Legacy token / desktop mode: no per-user permission store.
		return true
	}
	return identityHasPermission(identity, permission)
}

// canReadCouncilScope: council:read or council:configure, or (workspace
// scope) any workspace access.
func (a *API) canReadCouncilScope(r *http.Request, scope councilRolesScope) bool {
	if a.hasCouncilPermission(r, "council:read") || a.hasCouncilPermission(r, "council:configure") {
		return true
	}
	return scope.ScopeType == roles.ScopeWorkspace && a.canReadWorkspace(r.Context(), scope.ScopeID, requestOwnerID(r))
}

// canWriteCouncilScope: council:configure, or (workspace scope) workspace
// editor access (owner/admin).
func (a *API) canWriteCouncilScope(r *http.Request, scope councilRolesScope) bool {
	if a.hasCouncilPermission(r, "council:configure") {
		return true
	}
	return scope.ScopeType == roles.ScopeWorkspace && a.canEditWorkspace(r.Context(), scope.ScopeID, requestOwnerID(r))
}

func recordsToSlots(records []sqlite.CouncilRoleAssignmentRecord) []roles.Slot {
	slots := make([]roles.Slot, 0, len(records))
	for _, record := range records {
		slots = append(slots, roles.Slot{
			Role: record.Role, Ordinal: record.Ordinal,
			ProviderProfileID: record.ProviderProfileID, Model: record.Model,
			Params: json.RawMessage(record.ParamsJSON), Enabled: record.Enabled,
		})
	}
	return slots
}

func (a *API) councilRolesView(r *http.Request, scope councilRolesScope) (councilRolesResponse, error) {
	owner := requestOwnerID(r)
	set, records, found, err := a.councilRoles.LoadSet(r.Context(), owner, scope.ScopeType, scope.ScopeID)
	if err != nil {
		return councilRolesResponse{}, err
	}
	version := 0
	stored := []roles.Slot{}
	if found {
		version = set.Version
		stored = recordsToSlots(records)
	}
	resolver := a.councilResolver()
	workspaceID := ""
	if scope.ScopeType == roles.ScopeWorkspace {
		workspaceID = scope.ScopeID
	}
	source, err := resolver.EffectiveSource(r.Context(), owner, workspaceID)
	if err != nil {
		return councilRolesResponse{}, err
	}
	preview, err := resolver.Resolve(r.Context(), owner, workspaceID)
	if err != nil {
		return councilRolesResponse{}, err
	}
	return councilRolesResponse{
		ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, Version: version,
		EffectiveSource: source, Roles: stored, ResolvedPreview: preview, Warnings: []roles.Issue{},
	}, nil
}

func (a *API) getCouncilRoles(w http.ResponseWriter, r *http.Request) {
	if !a.councilRolesReady(w) {
		return
	}
	scope, ok := parseCouncilScope(w, r.URL.Query().Get("scope_type"), r.URL.Query().Get("scope_id"))
	if !ok {
		return
	}
	if !a.canReadCouncilScope(r, scope) {
		writeErr(w, http.StatusForbidden, "forbidden", "council:read permission or workspace access required")
		return
	}
	view, err := a.councilRolesView(r, scope)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	writeData(w, http.StatusOK, view)
}

func ifMatchVersion(r *http.Request, bodyVersion *int) int {
	if header := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`); header != "" {
		if version, err := strconv.Atoi(header); err == nil {
			return version
		}
	}
	if bodyVersion != nil {
		return *bodyVersion
	}
	return 0
}

func writeValidationFailed(w http.ResponseWriter, issues []roles.Issue) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "validation_failed",
			"message": "council role assignment is invalid",
			"issues":  issues,
		},
		"request_id": strconv.FormatInt(time.Now().UnixNano(), 10),
	})
}

func (a *API) putCouncilRoles(w http.ResponseWriter, r *http.Request) {
	if !a.councilRolesReady(w) {
		return
	}
	var in councilRolesPutRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body must be JSON")
		return
	}
	scope, ok := parseCouncilScope(w, in.ScopeType, in.ScopeID)
	if !ok {
		return
	}
	if !a.canWriteCouncilScope(r, scope) {
		writeErr(w, http.StatusForbidden, "forbidden", "council:configure permission or workspace editor access required")
		return
	}
	owner := requestOwnerID(r)
	profiles, err := a.councilProfiles(r.Context(), owner)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	if in.Slots == nil {
		in.Slots = []roles.Slot{}
	}
	if errs, _ := roles.ValidateSet(profiles, in.Slots); len(errs) > 0 {
		writeValidationFailed(w, errs)
		return
	}
	records := make([]sqlite.CouncilRoleAssignmentRecord, 0, len(in.Slots))
	for _, slot := range in.Slots {
		id, err := newApprovalNonce()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
			return
		}
		records = append(records, sqlite.CouncilRoleAssignmentRecord{
			ID: "cra-" + id, Role: slot.Role, Ordinal: slot.Ordinal,
			ProviderProfileID: slot.ProviderProfileID, Model: slot.Model,
			ParamsJSON: []byte(slot.Params), Enabled: slot.Enabled,
		})
	}
	_, err = a.councilRoles.ReplaceSet(r.Context(), owner, scope.ScopeType, scope.ScopeID, ifMatchVersion(r, in.Version), records, owner)
	if errors.Is(err, sqlite.ErrCouncilSetConflict) {
		writeErr(w, http.StatusConflict, "version_conflict", "the assignment set changed since it was read; reload and retry")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	view, err := a.councilRolesView(r, scope)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	writeData(w, http.StatusOK, view)
}

func (a *API) validateCouncilRoles(w http.ResponseWriter, r *http.Request) {
	if !a.councilRolesReady(w) {
		return
	}
	var in councilRolesPutRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body must be JSON")
		return
	}
	scope, ok := parseCouncilScope(w, in.ScopeType, in.ScopeID)
	if !ok {
		return
	}
	if !a.canWriteCouncilScope(r, scope) {
		writeErr(w, http.StatusForbidden, "forbidden", "council:configure permission or workspace editor access required")
		return
	}
	profiles, err := a.councilProfiles(r.Context(), requestOwnerID(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	errs, warnings := roles.ValidateSet(profiles, in.Slots)
	if errs == nil {
		errs = []roles.Issue{}
	}
	if warnings == nil {
		warnings = []roles.Issue{}
	}
	writeData(w, http.StatusOK, map[string]any{"valid": len(errs) == 0, "errors": errs, "warnings": warnings})
}

func (a *API) deleteCouncilRoles(w http.ResponseWriter, r *http.Request) {
	if !a.councilRolesReady(w) {
		return
	}
	scope, ok := parseCouncilScope(w, r.URL.Query().Get("scope_type"), r.URL.Query().Get("scope_id"))
	if !ok {
		return
	}
	if scope.ScopeType != roles.ScopeWorkspace {
		writeErr(w, http.StatusBadRequest, "invalid_scope", "only a workspace scope can be deleted; it then falls back to the global set")
		return
	}
	if !a.canWriteCouncilScope(r, scope) {
		writeErr(w, http.StatusForbidden, "forbidden", "council:configure permission or workspace editor access required")
		return
	}
	if err := a.councilRoles.DeleteSet(r.Context(), requestOwnerID(r), scope.ScopeType, scope.ScopeID); err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	view, err := a.councilRolesView(r, scope)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "council_roles_failed", err.Error())
		return
	}
	writeData(w, http.StatusOK, view)
}
