package council

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aicouncil/aicouncil/internal/app/aimodel"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

// AI model registry endpoints (Part B P4). Permissions follow the council
// roles pattern: reads accept provider:read or ai_model:manage, workspace
// reads accept model_workspace:read or ai_model:manage, writes require
// ai_model:manage. In legacy token mode (no identity) all checks pass, as
// with hasCouncilPermission.

const (
	permAIModelManage = "ai_model:manage"
	permAIModelRead   = "provider:read"
	permModelWSRead   = "model_workspace:read"
)

func (a *API) aiModelsReady(w http.ResponseWriter) bool {
	if a.aiModels == nil {
		writeErr(w, http.StatusServiceUnavailable, "ai_models_unavailable", "ai model registry is not configured")
		return false
	}
	return true
}

func (a *API) canReadAIModels(r *http.Request) bool {
	return a.hasCouncilPermission(r, permAIModelRead) || a.hasCouncilPermission(r, permAIModelManage)
}

func (a *API) canManageAIModels(r *http.Request) bool {
	return a.hasCouncilPermission(r, permAIModelManage)
}

func (a *API) canReadModelWorkspace(r *http.Request) bool {
	return a.hasCouncilPermission(r, permModelWSRead) || a.hasCouncilPermission(r, permAIModelManage)
}

func writeAIModelErr(w http.ResponseWriter, err error) {
	var se *aimodel.Error
	if errors.As(err, &se) {
		writeErr(w, se.Status, se.Code, se.Msg)
		return
	}
	writeErr(w, http.StatusInternalServerError, "ai_model_failed", err.Error())
}

func (a *API) listAIModels(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canReadAIModels(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "provider:read or ai_model:manage permission required")
		return
	}
	q := r.URL.Query()
	items, err := a.aiModels.List(r.Context(), sqlite.AIModelFilter{ProviderProfileID: q.Get("provider_profile_id"), Status: q.Get("status")})
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	if items == nil {
		items = []aimodel.View{}
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) createAIModel(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canManageAIModels(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "ai_model:manage permission required")
		return
	}
	var in aimodel.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body must be JSON")
		return
	}
	v, err := a.aiModels.Create(r.Context(), requestOwnerID(r), in)
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	writeData(w, http.StatusCreated, v)
}

func (a *API) getAIModel(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canReadAIModels(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "provider:read or ai_model:manage permission required")
		return
	}
	v, err := a.aiModels.Get(r.Context(), pathvar.Vars(r)["id"])
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	writeData(w, http.StatusOK, v)
}

func (a *API) patchAIModel(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canManageAIModels(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "ai_model:manage permission required")
		return
	}
	var in aimodel.UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body must be JSON")
		return
	}
	v, err := a.aiModels.Update(r.Context(), pathvar.Vars(r)["id"], in)
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	writeData(w, http.StatusOK, v)
}

func (a *API) deleteAIModel(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canManageAIModels(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "ai_model:manage permission required")
		return
	}
	v, err := a.aiModels.Disable(r.Context(), pathvar.Vars(r)["id"])
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	writeData(w, http.StatusOK, v)
}

func (a *API) getAIModelWorkspace(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canReadModelWorkspace(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "model_workspace:read or ai_model:manage permission required")
		return
	}
	v, err := a.aiModels.GetWorkspace(r.Context(), pathvar.Vars(r)["id"])
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	writeData(w, http.StatusOK, v)
}

func (a *API) retryAIModelWorkspace(w http.ResponseWriter, r *http.Request) {
	if !a.aiModelsReady(w) {
		return
	}
	if !a.canManageAIModels(r) {
		writeErr(w, http.StatusForbidden, "forbidden", "ai_model:manage permission required")
		return
	}
	v, err := a.aiModels.RetryWorkspace(r.Context(), pathvar.Vars(r)["id"])
	if err != nil {
		writeAIModelErr(w, err)
		return
	}
	writeData(w, http.StatusOK, v)
}
