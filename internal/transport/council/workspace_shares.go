package council

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	workspaceaccess "github.com/aicouncil/aicouncil/internal/security/workspaceaccess"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

type shareRequest struct {
	Access workspaceaccess.Access `json:"access"`
}

func (a *API) WorkspaceShareRoutes() []rest.Route {
	return []rest.Route{
		{Method:http.MethodGet, Path:"/api/v1/workspaces/:id/members", Handler:a.searchWorkspaceMembers},
		{Method: http.MethodGet, Path: "/api/v1/workspaces/:id/shares", Handler: a.listWorkspaceShares},
		{Method: http.MethodPut, Path: "/api/v1/workspaces/:id/shares/:subject", Handler: a.putWorkspaceShare},
		{Method: http.MethodDelete, Path: "/api/v1/workspaces/:id/shares/:subject", Handler: a.deleteWorkspaceShare},
	}
}

func (a *API) searchWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	if a.workspaceAccess == nil { writeErr(w,http.StatusServiceUnavailable,"sharing_unavailable","workspace sharing is unavailable"); return }
	prefix := strings.TrimSpace(r.URL.Query().Get("q"))
	members, err := a.workspaceAccess.SearchMembers(r.Context(),pathvar.Vars(r)["id"],requestOwnerID(r),prefix)
	if err != nil { a.writeShareError(w,err); return }
	writeData(w,http.StatusOK,members)
}

func (a *API) listWorkspaceShares(w http.ResponseWriter, r *http.Request) {
	if a.workspaceAccess == nil {
		writeErr(w, http.StatusServiceUnavailable, "sharing_unavailable", "workspace sharing is unavailable")
		return
	}
	shares, err := a.workspaceAccess.List(r.Context(), pathvar.Vars(r)["id"], requestOwnerID(r))
	if err != nil {
		a.writeShareError(w, err)
		return
	}
	writeData(w, http.StatusOK, shares)
}

func (a *API) putWorkspaceShare(w http.ResponseWriter, r *http.Request) {
	if a.workspaceAccess == nil {
		writeErr(w, http.StatusServiceUnavailable, "sharing_unavailable", "workspace sharing is unavailable")
		return
	}
	var in shareRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_share", "access must be viewer or editor")
		return
	}
	subject, err := url.PathUnescape(pathvar.Vars(r)["subject"])
	if err != nil || strings.TrimSpace(subject) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_share", "member subject is invalid")
		return
	}
	if err := a.workspaceAccess.Grant(r.Context(), pathvar.Vars(r)["id"], requestOwnerID(r), subject, in.Access); err != nil {
		a.writeShareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteWorkspaceShare(w http.ResponseWriter, r *http.Request) {
	if a.workspaceAccess == nil {
		writeErr(w, http.StatusServiceUnavailable, "sharing_unavailable", "workspace sharing is unavailable")
		return
	}
	subject, err := url.PathUnescape(pathvar.Vars(r)["subject"])
	if err != nil || strings.TrimSpace(subject) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_share", "member subject is invalid")
		return
	}
	if err := a.workspaceAccess.Revoke(r.Context(), pathvar.Vars(r)["id"], requestOwnerID(r), subject); err != nil {
		a.writeShareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) writeShareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspaceaccess.ErrInvalidShare):
		writeErr(w, http.StatusBadRequest, "invalid_share", "access must be viewer or editor")
	case errors.Is(err, workspaceaccess.ErrTargetUserNotFound):
		writeErr(w, http.StatusNotFound, "member_not_found", "active member not found")
	case errors.Is(err, workspaceaccess.ErrWorkspaceNotFound), errors.Is(err, workspaceaccess.ErrForbidden):
		writeErr(w, http.StatusNotFound, "not_found", "workspace not found")
	default:
		writeInternal(w)
	}
}
