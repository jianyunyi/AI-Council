package council

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"github.com/aicouncil/aicouncil/internal/app/group"
)

// Part B P7 review endpoints. Same conventions as requirement_handlers.go:
// groupActor/groupOK/groupFail, pathvar ids, membership enforced in the
// service (non-members get 404 group_not_found).

func (a *API) startGroupReview(w http.ResponseWriter, r *http.Request) {
	var in group.StartReviewInput
	if r.ContentLength > 0 {
		if err := groupDecode(r, &in); err != nil {
			a.groupBadRequest(w, "invalid JSON body")
			return
		}
	}
	vars := pathvar.Vars(r)
	v, err := a.groups.StartReview(r.Context(), a.groupActor(r), vars["id"], vars["rid"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) getGroupReview(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	v, err := a.groups.GetReview(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) submitGroupReviewDecision(w http.ResponseWriter, r *http.Request) {
	var in group.SubmitDecisionInput
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	vars := pathvar.Vars(r)
	v, err := a.groups.SubmitDecision(r.Context(), a.groupActor(r), vars["id"], vars["rid"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) forceCloseGroupReview(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	v, err := a.groups.ForceCloseReview(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) reviseGroupRequirement(w http.ResponseWriter, r *http.Request) {
	var in group.ReviseInput
	if r.ContentLength > 0 {
		if err := groupDecode(r, &in); err != nil {
			a.groupBadRequest(w, "invalid JSON body")
			return
		}
	}
	vars := pathvar.Vars(r)
	v, err := a.groups.ReviseRequirement(r.Context(), a.groupActor(r), vars["id"], vars["rid"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}
