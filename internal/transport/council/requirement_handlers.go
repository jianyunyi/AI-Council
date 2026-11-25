package council

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"github.com/aicouncil/aicouncil/internal/app/group"
)

// Part B P6 requirement endpoints. Same conventions as group_handlers.go:
// groupActor/groupOK/groupFail, pathvar ids, membership enforced in the
// service (non-members get 404 group_not_found).

func (a *API) listGroupRequirements(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt64(r, "limit")
	if err != nil {
		a.groupBadRequest(w, "invalid numeric query parameter")
		return
	}
	page, err := a.groups.ListRequirements(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], group.ListRequirementsInput{
		State:            r.URL.Query().Get("state"),
		AssigneeMemberID: r.URL.Query().Get("assignee_member_id"),
		Cursor:           r.URL.Query().Get("cursor"),
		Limit:            int(limit),
	})
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, page)
}

func (a *API) getGroupRequirement(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	v, err := a.groups.GetRequirement(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) retryGroupRequirement(w http.ResponseWriter, r *http.Request) {
	vars := pathvar.Vars(r)
	v, err := a.groups.RetryRequirement(r.Context(), a.groupActor(r), vars["id"], vars["rid"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}

func (a *API) cancelGroupRequirement(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if err := groupDecode(r, &in); err != nil {
			a.groupBadRequest(w, "invalid JSON body")
			return
		}
	}
	vars := pathvar.Vars(r)
	v, err := a.groups.CancelRequirement(r.Context(), a.groupActor(r), vars["id"], vars["rid"], in.Reason)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, v)
}
