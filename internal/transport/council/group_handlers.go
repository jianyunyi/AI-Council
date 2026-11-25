package council

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"github.com/aicouncil/aicouncil/internal/app/group"
)

const (
	PermGroupCreate    = "group:create"
	PermGroupReadAny   = "group:read:any"
	PermGroupManageAny = "group:manage:any"

	sseReplayPage   = 500
	sseHeartbeat    = 20 * time.Second
	sseMaxReplayAll = 5000 // a fresh connection with no Last-Event-ID replays at most this many events
)

// legacyGroupUserID is the stable synthetic user for legacy static-token
// mode (WIRING W4): that mode has no per-user identity, so requestOwnerID
// returns "". Group ownership and membership stay consistent within the
// mode by attributing everything to this one user; hasCouncilPermission
// passes every check there as well.
const legacyGroupUserID = "legacy"

// ---- WIRING W3/W4/W5: the only places that touch existing helpers ----

func (a *API) groupActor(r *http.Request) group.Actor {
	userID := requestOwnerID(r) // WIRING W4 (no currentUserID helper exists)
	if userID == "" {
		userID = legacyGroupUserID
	}
	return group.Actor{
		UserID:         userID,
		CanCreateGroup: a.hasCouncilPermission(r, PermGroupCreate),    // WIRING W5
		ReadAny:        a.hasCouncilPermission(r, PermGroupReadAny),   // WIRING W5
		ManageAny:      a.hasCouncilPermission(r, PermGroupManageAny), // WIRING W5
	}
}

func (a *API) groupOK(w http.ResponseWriter, status int, data any) {
	writeData(w, status, data) // WIRING W3
}

func (a *API) groupFail(w http.ResponseWriter, err error) {
	var ge *group.Error
	if errors.As(err, &ge) {
		writeErr(w, ge.Status, ge.Code, ge.Msg) // WIRING W3
		return
	}
	log.Printf("group api: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal error") // WIRING W3
}

func (a *API) groupBadRequest(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusBadRequest, "bad_request", msg) // WIRING W3
}

// ---- helpers ----

func groupDecode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func queryInt64(r *http.Request, key string) (int64, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

// ---- groups ----

func (a *API) listGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := queryInt64(r, "limit")
	if err != nil {
		a.groupBadRequest(w, "invalid limit")
		return
	}
	items, next, err := a.groups.ListGroups(r.Context(), a.groupActor(r), group.ListGroupsInput{
		Status: q.Get("status"), Q: q.Get("q"), Cursor: q.Get("cursor"), Limit: int(limit)})
	if err != nil {
		a.groupFail(w, err)
		return
	}
	var nextCursor any
	if next != "" {
		nextCursor = next
	}
	a.groupOK(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nextCursor})
}

func (a *API) createGroup(w http.ResponseWriter, r *http.Request) {
	var in group.CreateGroupInput
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	g, err := a.groups.CreateGroup(r.Context(), a.groupActor(r), in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusCreated, g)
}

func (a *API) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := a.groups.GetGroup(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, g)
}

func (a *API) patchGroup(w http.ResponseWriter, r *http.Request) {
	var in group.UpdateGroupInput
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	g, err := a.groups.UpdateGroup(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, g)
}

func (a *API) archiveGroup(w http.ResponseWriter, r *http.Request) {
	g, err := a.groups.ArchiveGroup(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"])
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, g)
}

// ---- members ----

func (a *API) listGroupMembers(w http.ResponseWriter, r *http.Request) {
	inc := r.URL.Query().Get("include_removed") == "true"
	items, err := a.groups.ListMembers(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], inc)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) addGroupMember(w http.ResponseWriter, r *http.Request) {
	var in group.AddMemberInput
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	m, err := a.groups.AddMember(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusCreated, m)
}

func (a *API) patchGroupMember(w http.ResponseWriter, r *http.Request) {
	var in group.UpdateMemberInput
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	v := pathvar.Vars(r)
	m, err := a.groups.UpdateMember(r.Context(), a.groupActor(r), v["id"], v["memberId"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, m)
}

func (a *API) removeGroupMember(w http.ResponseWriter, r *http.Request) {
	v := pathvar.Vars(r)
	if err := a.groups.RemoveMember(r.Context(), a.groupActor(r), v["id"], v["memberId"]); err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, map[string]any{"removed": true})
}

// ---- messages ----

func (a *API) listGroupMessages(w http.ResponseWriter, r *http.Request) {
	after, err1 := queryInt64(r, "after_seq")
	before, err2 := queryInt64(r, "before_seq")
	limit, err3 := queryInt64(r, "limit")
	if err1 != nil || err2 != nil || err3 != nil {
		a.groupBadRequest(w, "invalid numeric query parameter")
		return
	}
	page, err := a.groups.ListMessages(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], group.ListMessagesInput{
		AfterSeq: after, BeforeSeq: before, Limit: int(limit), RequirementID: r.URL.Query().Get("requirement_id")})
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, page)
}

func (a *API) postGroupMessage(w http.ResponseWriter, r *http.Request) {
	var in group.PostMessageInput
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	res, err := a.groups.PostMessage(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], in)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	status := http.StatusCreated
	if res.Duplicate {
		status = http.StatusOK
	}
	a.groupOK(w, status, res)
}

func (a *API) patchGroupMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BodyMD string `json:"body_md"`
	}
	if err := groupDecode(r, &in); err != nil {
		a.groupBadRequest(w, "invalid JSON body")
		return
	}
	v := pathvar.Vars(r)
	m, err := a.groups.EditMessage(r.Context(), a.groupActor(r), v["id"], v["mid"], in.BodyMD)
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, m)
}

func (a *API) resolveGroupMentions(w http.ResponseWriter, r *http.Request) {
	items, err := a.groups.ResolveMentionCandidates(r.Context(), a.groupActor(r), pathvar.Vars(r)["id"], r.URL.Query().Get("q"))
	if err != nil {
		a.groupFail(w, err)
		return
	}
	a.groupOK(w, http.StatusOK, map[string]any{"items": items})
}

// ---- SSE ----

// writeGroupSSE writes one event. WIRING W10 (adapted): the task stream sends
// `event:` lines with a raw payload, but the group stream deliberately uses
// id + data only, with data carrying the {seq,type,ts,data} envelope — the
// group frontend consumes it through EventSource.onmessage, which never
// fires for named events.
func writeGroupSSE(w io.Writer, ev group.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, b)
	return err
}

// groupEvents streams group events. Order of operations:
//  1. authorize (404 for non-members)
//  2. subscribe to the live hub BEFORE replay, so nothing is missed between the two
//  3. replay durable events with seq > Last-Event-ID (header, or ?after_seq= for EventSource reconnect shims)
//  4. forward live events, skipping seq <= last replayed (dedupe)
//
// The stream ends when the client disconnects, the subscriber falls behind
// (hub closes C; the client reconnects and replays), or the member is removed (Kick).
func (a *API) groupEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gid := pathvar.Vars(r)["id"]
	actor := a.groupActor(r)
	if err := a.groups.AuthorizeRead(ctx, actor, gid); err != nil {
		a.groupFail(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "sse_unsupported", "streaming unsupported") // WIRING W3
		return
	}

	var last int64
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		v, err := strconv.ParseInt(h, 10, 64)
		if err != nil || v < 0 {
			a.groupBadRequest(w, "invalid Last-Event-ID")
			return
		}
		last = v
	} else if v, err := queryInt64(r, "after_seq"); err != nil || v < 0 {
		a.groupBadRequest(w, "invalid after_seq")
		return
	} else {
		last = v
	}

	sub := a.groups.Hub().Subscribe(gid, actor.UserID)
	defer sub.Close()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	replayed := 0
	for {
		evs, err := a.groups.EventsAfter(ctx, gid, last, sseReplayPage)
		if err != nil {
			log.Printf("group sse replay %s: %v", gid, err)
			return
		}
		for _, ev := range evs {
			if err := writeGroupSSE(w, ev); err != nil {
				return
			}
			last = ev.Seq
			replayed++
		}
		flusher.Flush()
		if len(evs) < sseReplayPage || replayed >= sseMaxReplayAll {
			break
		}
	}

	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.C:
			if !open {
				return
			}
			if ev.Seq <= last {
				continue
			}
			if err := writeGroupSSE(w, ev); err != nil {
				return
			}
			last = ev.Seq
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
