package council

import (
	"encoding/json"
	"net/http"

	providerkeysvc "github.com/aicouncil/aicouncil/internal/security/providerkeys"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

type createProviderKeyRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

func (a *API) ProviderKeyRoutes() []rest.Route {
	return []rest.Route{
		{Method: http.MethodGet, Path: "/api/v1/providers/keys", Handler: a.listProviderKeys},
		{Method: http.MethodPost, Path: "/api/v1/providers/keys", Handler: a.createProviderKey},
		{Method: http.MethodDelete, Path: "/api/v1/providers/keys/:id", Handler: a.deleteProviderKey},
		{Method: http.MethodGet, Path: "/api/v1/providers/:id/models", Handler: a.listProviderModels},
	}
}

// listProviderModels returns the models a provider profile offers. Profiles
// are local configuration (no remote catalog is fetched), so the list is the
// profile's single configured model, always verified.
func (a *API) listProviderModels(w http.ResponseWriter, r *http.Request) {
	if a.providerKeys == nil {
		writeErr(w, http.StatusServiceUnavailable, "provider_keys_unavailable", "provider key storage is unavailable")
		return
	}
	id := pathvar.Vars(r)["id"]
	profiles, err := a.providerKeys.List(r.Context(), requestOwnerID(r))
	if err != nil {
		writeInternal(w)
		return
	}
	for _, profile := range profiles {
		if profile.ID == id {
			writeData(w, http.StatusOK, map[string]any{
				"provider_profile_id": profile.ID,
				"provider":            profile.Provider,
				"source":              "profile",
				"models":              []map[string]any{{"id": profile.Model, "verified": true}},
			})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "provider profile not found")
}

func (a *API) listProviderKeys(w http.ResponseWriter, r *http.Request) {
	if a.providerKeys == nil {
		writeErr(w, http.StatusServiceUnavailable, "provider_keys_unavailable", "provider key storage is unavailable")
		return
	}
	profiles, err := a.providerKeys.List(r.Context(), requestOwnerID(r))
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, profiles)
}

func (a *API) createProviderKey(w http.ResponseWriter, r *http.Request) {
	if a.providerKeys == nil {
		writeErr(w, http.StatusServiceUnavailable, "provider_keys_unavailable", "provider key storage is unavailable")
		return
	}
	var in createProviderKeyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_provider_key", "provider, model and api_key are required")
		return
	}
	profile, err := a.providerKeys.Create(r.Context(), requestOwnerID(r), in.Provider, in.Model, in.APIKey)
	if err != nil {
		if err == providerkeysvc.ErrInvalidProfile {
			writeErr(w, http.StatusBadRequest, "invalid_provider_key", "provider, model or api_key is invalid")
			return
		}
		writeInternal(w)
		return
	}
	writeData(w, http.StatusCreated, profile)
}

func (a *API) deleteProviderKey(w http.ResponseWriter, r *http.Request) {
	if a.providerKeys == nil {
		writeErr(w, http.StatusServiceUnavailable, "provider_keys_unavailable", "provider key storage is unavailable")
		return
	}
	if err := a.providerKeys.Delete(r.Context(), requestOwnerID(r), pathvar.Vars(r)["id"]); err != nil {
		if err == providerkeysvc.ErrNotFound {
			writeErr(w, http.StatusNotFound, "not_found", "provider key not found")
			return
		}
		writeInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
