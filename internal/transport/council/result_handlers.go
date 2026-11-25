package council

import (
	"io"
	"net/http"
	"strings"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"github.com/aicouncil/aicouncil/internal/app/group"
)

// Part B P9 result endpoints. Same conventions as review_handlers.go:
// groupActor/groupOK/groupFail, pathvar ids, membership and manifest
// scoping enforced in the service (non-members get 404
// group_not_found; artifacts outside the requirement's manifest get
// 404 artifact_not_found).

func (a *API) listRequirementResults(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	items, err := a.groups.ListResults(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) startResultReview(w http.ResponseWriter, r *http.Request) {
	var in group.StartResultReviewInput
	if r.ContentLength > 0 {
		if err := groupDecode(r, &in); err != nil {
			a.groupBadRequest(w, "invalid JSON body")
			return
		}
	}
	vars := pathvar.Vars(r)
	v, err := a.groups.StartResultReview(r.Context(), a.groupActor(r), vars["id"], vars["rid"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

// downloadGroupArtifact streams a copied artifact from the group's
// manifest. Copied artifacts are model-produced content, so the
// response is hardened: nosniff, a sandbox CSP, and inline display
// only for text/plain — everything else downloads as an attachment.
func (a *API) downloadGroupArtifact(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	view, rc, err := a.groups.OpenArtifact(r.Context(), a.groupActor(r), vars["id"], vars["rid"], vars["aid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	defer func() { _ = rc.Close() }()
	mediaType := view.MediaType
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	disposition := "attachment"
	if mediaType == "text/plain" {
		disposition = "inline"
	}
	header := w.Header()
	header.Set("Content-Type", mediaType)
	header.Set("Content-Disposition", disposition+"; filename=\""+strings.ReplaceAll(view.Name, "\"", "")+"\"")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "sandbox")
	if view.SHA256 != "" {
		header.Set("X-Content-SHA256", view.SHA256)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}
