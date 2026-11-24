package council

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"github.com/aicouncil/aicouncil/internal/app/group"
)

// Part B P8 endpoints: the D1 human approval and the two retries.
// Same conventions as review_handlers.go.

func (a *API) approveGroupRequirement(w http.ResponseWriter, r *http.Request) {
	var in group.ApproveInput
	if r.ContentLength > 0 {
		if err := groupDecode(r, &in); err != nil {
			a.groupBadRequest(w, "invalid JSON body")
			return
		}
	}
	vars := pathvar.Vars(r)
	v, err := a.groups.Approve(r.Context(), a.groupActor(r), vars["id"], vars["rid"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) retryGroupPushdown(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	v, err := a.groups.RetryPushdown(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) retryGroupRedTeam(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	v, err := a.groups.RetryRedTeam(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}
