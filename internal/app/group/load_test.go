package group

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// P10.1: Part B invariants under load and contention. Failures here are
// presumed REAL until proven otherwise — these tests assert the design's
// guarantees (gapless per-group sequences, exactly-once jobs, CAS
// single-winner transitions, blindness, cross-group isolation).

// 1. Per-group sequence allocation under concurrency: many groups × many posters, mixed chat and
// dispatch (which allocates extra paired message+event seqs), with concurrent idempotent retries.
func TestLoadConcurrentPostsGaplessPerGroup(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 4 * n, ais: 2, aiPool: 3, ops: 4, hubBuffer: 256})
	perPoster := 10 * n
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, g := range e.groups {
		for pi, op := range g.ops {
			wg.Add(1)
			go func(g *ldGroup, pi int, op Actor) {
				defer wg.Done()
				for k := 0; k < perPoster; k++ {
					in := PostMessageInput{BodyMD: fmt.Sprintf("g%d p%d m%d", g.idx, pi, k), ClientMsgID: fmt.Sprintf("p%d-%d", pi, k)}
					if k%5 == 4 {
						h := g.aiHandles[k%len(g.aiHandles)]
						in.Intent, in.BodyMD = "dispatch", ldDispatchBody(g, h, fmt.Sprintf("%d-%d", pi, k))
					}
					if k%3 != 0 {
						if _, err := e.svc.PostMessage(ctx, op, g.id, in); err != nil {
							t.Errorf("g%d post: %v", g.idx, err)
						}
						continue
					}
					// The same client_msg_id posted twice at once must yield ONE message.
					var inner sync.WaitGroup
					res := make([]*PostMessageResult, 2)
					errs := make([]error, 2)
					for x := 0; x < 2; x++ {
						inner.Add(1)
						go func(x int) { defer inner.Done(); res[x], errs[x] = e.svc.PostMessage(ctx, op, g.id, in) }(x)
					}
					inner.Wait()
					if errs[0] != nil || errs[1] != nil {
						t.Errorf("g%d concurrent retry: %v / %v", g.idx, errs[0], errs[1])
						continue
					}
					if res[0].Message.ID != res[1].Message.ID || res[0].Duplicate == res[1].Duplicate {
						t.Errorf("g%d idempotency broken: %+v / %+v", g.idx, res[0], res[1])
					}
				}
			}(g, pi, op)
		}
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	dispatchPerPoster := 0
	for k := 0; k < perPoster; k++ {
		if k%5 == 4 {
			dispatchPerPoster++
		}
	}
	owners := e.ridOwners(t)
	for _, g := range e.groups {
		msgs, evs := e.checkGroup(t, g, owners)
		var userMsgs, cards, created int
		for _, m := range msgs {
			switch {
			case m.AuthorType == AuthorUser:
				userMsgs++
			case m.Kind == KindDispatchCard:
				cards++
			}
		}
		for _, ev := range evs {
			if ev.Type == "message.created" {
				created++
			}
		}
		wantUser, wantReq := len(g.ops)*perPoster, len(g.ops)*dispatchPerPoster
		if userMsgs != wantUser || cards != wantReq || created != len(msgs) {
			t.Fatalf("g%d: user=%d/%d cards=%d/%d created-events=%d msgs=%d", g.idx, userMsgs, wantUser, cards, wantReq, created, len(msgs))
		}
		if reqs := e.requirements(t, g); len(reqs) != wantReq {
			t.Fatalf("g%d: requirements=%d want %d (duplicate dispatch?)", g.idx, len(reqs), wantReq)
		}
	}
}

// 2. Job-queue contention: several workers, many groups, shared models. Every requirement is
// proposed exactly once, a serial key (group:assignee) is never run twice at the same time, and
// nothing is left queued or running.
func TestLoadJobQueueMultiWorker(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 3 * n, ais: 3, aiPool: 4, ops: 1, hubBuffer: 1024, delay: 2 * time.Millisecond})
	perGroup := 6 * n
	var wg sync.WaitGroup
	for _, g := range e.groups {
		wg.Add(1)
		go func(g *ldGroup) {
			defer wg.Done()
			for r := 0; r < perGroup; r++ {
				e.dispatch(t, g, g.owner, g.aiHandles[r%len(g.aiHandles)], fmt.Sprintf("q%d", r))
			}
		}(g)
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	e.runWorkers(t, 4, e.allIn(t, ReqProposed), 2*time.Minute)

	total := len(e.groups) * perGroup
	e.prov.mu.Lock()
	if e.prov.total != total || len(e.prov.proposalCalls) != total {
		t.Errorf("proposal calls=%d markers=%d want %d", e.prov.total, len(e.prov.proposalCalls), total)
	}
	for mk, c := range e.prov.proposalCalls {
		if c != 1 {
			t.Errorf("requirement %s proposed %d times", mk, c)
		}
	}
	for key, m := range e.prov.maxInflight {
		if m > 1 {
			t.Errorf("serial key %s ran %d jobs concurrently", key, m)
		}
	}
	e.prov.mu.Unlock()
	if open := e.ldOpenJobs(t); open != 0 {
		t.Fatalf("%d jobs left queued/running", open)
	}
	owners := e.ridOwners(t)
	for _, g := range e.groups {
		e.checkGroup(t, g, owners)
	}
}

// 3. SSE fan-out: many subscribers per group run the SSE handler's protocol (subscribe, replay
// from the DB, live with dedupe, reconnect on close). With a small hub buffer some get dropped
// and must resume. Every client ends with exactly 1..N of its own group, with no gaps or foreign
// events. A subscriber that never reads is disconnected, not blocked on.
func TestLoadSSEFanoutAndResume(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 3 * n, ais: 1, aiPool: 1, ops: 3, hubBuffer: 32})
	perPoster := 70 * n
	const clientsPerGroup = 4
	type result struct {
		seqs                      []int64
		gaps, foreign, reconnects int
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	targets := make([]atomic.Int64, len(e.groups))
	results := make([][]*result, len(e.groups))
	slow := make([]*Subscription, len(e.groups))
	var clients sync.WaitGroup
	for gi, g := range e.groups {
		slow[gi] = e.svc.Hub().Subscribe(g.id, "u_slow")
		for c := 0; c < clientsPerGroup; c++ {
			r := &result{}
			results[gi] = append(results[gi], r)
			clients.Add(1)
			go func(gid string, target *atomic.Int64, r *result, uid string) {
				defer clients.Done()
				var last int64
				accept := func(ev Event) {
					if ev.Seq <= last {
						return
					}
					if ev.Seq != last+1 {
						r.gaps++
					}
					if ev.Type == "message.created" {
						var mv struct {
							GroupID string `json:"group_id"`
						}
						_ = json.Unmarshal(ev.Data, &mv)
						if mv.GroupID != gid {
							r.foreign++
						}
					}
					r.seqs = append(r.seqs, ev.Seq)
					last = ev.Seq
				}
				reached := func() bool { tg := target.Load(); return tg > 0 && last >= tg }
				for ctx.Err() == nil && !reached() {
					sub := e.svc.Hub().Subscribe(gid, uid) // subscribe BEFORE replay
					evs, err := e.svc.EventsAfter(ctx, gid, last, 1_000_000)
					if err != nil {
						sub.Close()
						return
					}
					for _, ev := range evs {
						accept(ev)
					}
				live:
					for !reached() {
						select {
						case <-ctx.Done():
							break live
						case ev, ok := <-sub.C:
							if !ok {
								r.reconnects++
								break live
							}
							accept(ev)
						case <-time.After(50 * time.Millisecond):
						}
					}
					sub.Close()
				}
			}(g.id, &targets[gi], r, fmt.Sprintf("u_client_%d_%d", gi, c))
		}
	}
	var posters sync.WaitGroup
	for _, g := range e.groups {
		for pi, op := range g.ops {
			posters.Add(1)
			go func(g *ldGroup, pi int, op Actor) {
				defer posters.Done()
				for k := 0; k < perPoster; k++ {
					if _, err := e.svc.PostMessage(context.Background(), op, g.id, PostMessageInput{
						BodyMD: fmt.Sprintf("fan g%d p%d m%d", g.idx, pi, k), ClientMsgID: fmt.Sprintf("f%d-%d", pi, k)}); err != nil {
						t.Errorf("post: %v", err)
					}
				}
			}(g, pi, op)
		}
	}
	posters.Wait()
	for gi, g := range e.groups {
		gd, err := e.svc.GetGroup(context.Background(), g.owner, g.id)
		if err != nil {
			t.Fatal(err)
		}
		targets[gi].Store(gd.LastEventSeq)
	}
	clients.Wait()
	if ctx.Err() != nil {
		t.Fatal("clients did not catch up in time")
	}
	reconnects := 0
	for gi := range e.groups {
		want := targets[gi].Load()
		for ci, r := range results[gi] {
			reconnects += r.reconnects
			if r.gaps != 0 || r.foreign != 0 || int64(len(r.seqs)) != want {
				t.Fatalf("g%d client %d: gaps=%d foreign=%d got=%d want=%d", gi, ci, r.gaps, r.foreign, len(r.seqs), want)
			}
			for i, s := range r.seqs {
				if s != int64(i+1) {
					t.Fatalf("g%d client %d: seq %d at %d", gi, ci, s, i)
				}
			}
		}
		// The slow subscriber must have been disconnected (channel closed), not blocked on.
		closed := false
		timeout := time.After(time.Second)
	drain:
		for {
			select {
			case _, ok := <-slow[gi].C:
				if !ok {
					closed = true
					break drain
				}
			case <-timeout:
				break drain
			}
		}
		if !closed && want > 32 {
			t.Fatalf("g%d: slow subscriber was never disconnected", gi)
		}
		slow[gi].Close()
	}
	t.Logf("fan-out: %d reconnects exercised the resume path", reconnects)
}

// 4a. CAS: concurrent StartReview on the same proposed requirement opens exactly one round.
func TestLoadConcurrentReviewStartSingleRound(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 2 * n, ais: 2, aiPool: 3, ops: 2, hubBuffer: 1024})
	for _, g := range e.groups {
		for r := 0; r < 3; r++ {
			e.dispatch(t, g, g.owner, g.aiHandles[0], fmt.Sprintf("rv%d", r))
		}
	}
	e.runWorkers(t, 4, e.allIn(t, ReqProposed), time.Minute)
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, g := range e.groups {
		for _, req := range e.requirements(t, g) {
			var wins atomic.Int32
			actors := append([]Actor{g.owner, g.owner, g.owner, g.owner}, g.ops...)
			for _, a := range actors {
				wg.Add(1)
				go func(g *ldGroup, rid string, a Actor, wins *atomic.Int32) {
					defer wg.Done()
					_, err := e.svc.StartReview(ctx, a, g.id, rid, StartReviewInput{ReviewerMemberIDs: []string{g.member[g.aiHandles[1]]}})
					if err == nil {
						wins.Add(1)
					} else if !ldCodeIn(err, "review_already_open", "requirement_state_conflict", "version_conflict", "review_forbidden") {
						t.Errorf("unexpected StartReview error: %v", err)
					}
				}(g, req.ID, a, &wins)
			}
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("g%d %s: %d StartReview winners", g.idx, req.ID, wins.Load())
			}
			rv, err := e.svc.GetReview(ctx, g.owner, g.id, req.ID)
			if err != nil || len(rv.Rounds) != 1 || len(rv.Rounds[0].Decisions) != 1 {
				t.Fatalf("g%d %s: rounds=%+v err=%v", g.idx, req.ID, rv, err)
			}
		}
	}
	e.runWorkers(t, 4, e.allIn(t, ReqReviewPassed), time.Minute)
	owners := e.ridOwners(t)
	for _, g := range e.groups {
		e.checkGroup(t, g, owners)
	}
}

// 4b. CAS + D1: concurrent approvals by several humans create exactly one push-down, one task
// and one execution (single-use approval) per requirement.
func TestLoadConcurrentApproveSinglePushdown(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 2 * n, ais: 2, aiPool: 3, ops: 2, hubBuffer: 1024, pushdown: true})
	e.toAwaitingApproval(t, 2)
	ctx := context.Background()
	total := 0
	var wg sync.WaitGroup
	for _, g := range e.groups {
		for _, req := range e.requirements(t, g) {
			total++
			var wins atomic.Int32
			for _, a := range append([]Actor{g.owner}, g.ops...) {
				wg.Add(1)
				go func(g *ldGroup, rid string, a Actor, wins *atomic.Int32) {
					defer wg.Done()
					err := e.ldApprove(ctx, a, g, rid)
					if err == nil {
						wins.Add(1)
					} else if !ldCodeIn(err, "not_approvable", "version_conflict") {
						t.Errorf("unexpected Approve error: %v", err)
					}
				}(g, req.ID, a, &wins)
			}
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("g%d %s: %d approvals won", g.idx, req.ID, wins.Load())
			}
			if s := e.state(t, g, req.ID); s != ReqExecuting {
				t.Fatalf("g%d %s: state %s", g.idx, req.ID, s)
			}
		}
	}
	creates, _, executes := e.bridge.counts()
	if creates != total || executes != total {
		t.Fatalf("tasks=%d executes=%d want %d", creates, executes, total)
	}
	owners := e.ridOwners(t)
	for _, g := range e.groups {
		e.checkGroup(t, g, owners)
	}
}

// 4c. CAS: concurrent mirror applications of the same task status post exactly one card, and
// a concurrent final status finishes the requirement and queues exactly one result job (L6).
func TestLoadMirrorApplyDedupe(t *testing.T) {
	ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 1, ais: 2, aiPool: 2, ops: 1, hubBuffer: 1024, pushdown: true})
	e.toAwaitingApproval(t, 1)
	g := e.groups[0]
	ctx := context.Background()
	rid := e.requirements(t, g)[0].ID
	if err := e.ldApprove(ctx, g.owner, g, rid); err != nil {
		t.Fatal(err)
	}
	pd, err := e.svc.pd().LatestPushdown(ctx, rid)
	if err != nil || pd == nil || pd.TaskID == nil {
		t.Fatalf("pushdown: %+v %v", pd, err)
	}
	e.bridge.mu.Lock()
	hash := e.bridge.tasks[*pd.TaskID].planHash
	e.bridge.mu.Unlock()
	apply := func(state, phase string) {
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.svc.mirrorApply(ctx, g.id, rid, pd.ID, TaskStatus{TaskID: *pd.TaskID, State: state, Stage: state, Phase: phase, PlanHash: hash})
			}()
		}
		wg.Wait()
	}
	apply("VERIFYING", "running")
	e.bridge.mu.Lock()
	e.bridge.tasks[*pd.TaskID].state = "COMPLETED"
	e.bridge.mu.Unlock()
	apply("COMPLETED", "succeeded")
	if s := e.state(t, g, rid); s != ReqExecuted && s != ReqResultSubmitted {
		t.Fatalf("state %s", s)
	}
	msgs, evs := e.checkGroup(t, g, e.ridOwners(t))
	counts := map[string]int{}
	for _, m := range msgs {
		if m.Kind == KindTaskStatus {
			var p struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal(m.Payload, &p)
			counts[p.State]++
		}
	}
	collecting := 0
	for _, ev := range evs {
		if ev.Type == "result.collecting" {
			collecting++
		}
	}
	if counts["VERIFYING"] != 1 || counts["COMPLETED"] != 1 || collecting != 1 {
		t.Fatalf("task_status=%v result.collecting=%d", counts, collecting)
	}
}

// 4d. CAS + blindness: cancel racing a human verdict. The requirement always ends cancelled,
// no round stays open, and a superseded round never reveals the decision.
func TestLoadCancelRacesDecision(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: n, ais: 1, aiPool: 1, ops: 1, hubBuffer: 1024})
	for _, g := range e.groups {
		for r := 0; r < 8; r++ {
			e.dispatch(t, g, g.owner, g.aiHandles[0], fmt.Sprintf("cx%d", r))
		}
	}
	e.runWorkers(t, 2, e.allIn(t, ReqProposed), time.Minute)
	ctx := context.Background()
	for _, g := range e.groups {
		reviewer := g.ops[0]
		for _, req := range e.requirements(t, g) {
			if _, err := e.svc.StartReview(ctx, g.owner, g.id, req.ID,
				StartReviewInput{ReviewerMemberIDs: []string{g.member[reviewer.UserID]}}); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Add(2)
			go func(rid string) {
				defer wg.Done()
				if _, err := e.svc.CancelRequirement(ctx, g.owner, g.id, rid, ""); err != nil {
					t.Errorf("cancel: %v", err)
				}
			}(req.ID)
			go func(rid string) {
				defer wg.Done()
				_, err := e.svc.SubmitDecision(ctx, reviewer, g.id, rid, SubmitDecisionInput{Verdict: "approve"})
				if err != nil && !ldCodeIn(err, "no_open_review", "not_a_reviewer") {
					t.Errorf("decision: %v", err)
				}
			}(req.ID)
			wg.Wait()
			if s := e.state(t, g, req.ID); s != ReqCancelled {
				t.Fatalf("%s: state %s", req.ID, s)
			}
			rv, err := e.svc.GetReview(ctx, g.owner, g.id, req.ID)
			if err != nil || len(rv.Rounds) != 1 || rv.Rounds[0].Status != RoundClosed {
				t.Fatalf("%s: review %+v %v", req.ID, rv, err)
			}
			r0 := rv.Rounds[0]
			switch r0.Outcome {
			case OutcomeSuperseded:
				// Landed blindness semantics: the decision rows are
				// listed, but a superseded round never reveals a
				// verdict or rationale.
				if len(r0.Decisions) != 1 {
					t.Fatalf("%s: superseded round has %d decisions", req.ID, len(r0.Decisions))
				}
				for _, d := range r0.Decisions {
					if d.Verdict != "" || d.Rationale != "" {
						t.Fatalf("%s: superseded round revealed a decision: %+v", req.ID, d)
					}
				}
			case OutcomePassed:
				if len(r0.Decisions) != 1 || r0.Decisions[0].Verdict != VerdictApprove {
					t.Fatalf("%s: passed round must reveal its approve decision: %+v", req.ID, r0.Decisions)
				}
			default:
				t.Fatalf("%s: unexpected outcome %s", req.ID, r0.Outcome)
			}
		}
		e.checkGroup(t, g, e.ridOwners(t))
	}
}

// 5. No cross-group leakage while groups share the same AI models (and model workspaces):
// manifests list only a group's own task outputs, and foreign access is 404 at every layer.
func TestLoadCrossGroupIsolationSharedModels(t *testing.T) {
	n := ldScale(t)
	e := newLoadEnv(t, ldOpts{groups: 2 * n, ais: 2, aiPool: 2, ops: 1, hubBuffer: 1024, pushdown: true})
	e.toAwaitingApproval(t, 1)
	ctx := context.Background()
	rids := make([]string, len(e.groups))
	var wg sync.WaitGroup
	for gi, g := range e.groups {
		rids[gi] = e.requirements(t, g)[0].ID
		wg.Add(1)
		go func(gi int, g *ldGroup) {
			defer wg.Done()
			if err := e.ldApprove(ctx, g.owner, g, rids[gi]); err != nil {
				t.Errorf("approve g%d: %v", gi, err)
			}
		}(gi, g)
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	for gi, g := range e.groups {
		pd, err := e.svc.pd().LatestPushdown(ctx, rids[gi])
		if err != nil || pd == nil || pd.TaskID == nil {
			t.Fatalf("g%d pushdown: %v", gi, err)
		}
		e.arts.add(*pd.TaskID, ArtifactMeta{Ref: fmt.Sprintf("art-g%d", gi), Name: fmt.Sprintf("g%d-report.md", gi),
			Kind: "report", MediaType: "text/markdown", WorkspaceID: pd.ModelWorkspaceID},
			[]byte(fmt.Sprintf("# g%d", gi)))
		e.bridge.mu.Lock()
		e.bridge.tasks[*pd.TaskID].state = "COMPLETED"
		e.bridge.mu.Unlock()
		st, err := e.bridge.TaskStatus(ctx, *pd.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		e.svc.mirrorApply(ctx, g.id, rids[gi], pd.ID, st)
	}
	e.runWorkers(t, 4, e.allIn(t, ReqResultSubmitted), time.Minute)
	artIDs := make([]string, len(e.groups))
	for gi, g := range e.groups {
		res, err := e.svc.ListResults(ctx, g.owner, g.id, rids[gi])
		if err != nil || len(res) != 1 {
			t.Fatalf("g%d results: %v %v", gi, res, err)
		}
		prefix := fmt.Sprintf("g%d-", gi)
		for _, a := range res[0].Artifacts {
			if len(a.Name) < len(prefix) || a.Name[:len(prefix)] != prefix {
				t.Fatalf("g%d manifest contains foreign artifact %s", gi, a.Name)
			}
			if a.Status == "copied" {
				artIDs[gi] = a.ID
			}
		}
		if artIDs[gi] == "" {
			t.Fatalf("g%d: own artifact not copied", gi)
		}
	}
	for a := range e.groups {
		b := (a + 1) % len(e.groups)
		ga, gb := e.groups[a], e.groups[b]
		checks := []struct {
			err  error
			want string
		}{
			{func() error { _, err := e.svc.GetRequirement(ctx, ga.owner, gb.id, rids[b]); return err }(), "group_not_found"},
			{func() error { _, err := e.svc.GetRequirement(ctx, ga.owner, ga.id, rids[b]); return err }(), "requirement_not_found"},
			{func() error { _, err := e.svc.ListResults(ctx, ga.owner, ga.id, rids[b]); return err }(), "requirement_not_found"},
			{func() error { _, err := e.svc.ListMessages(ctx, ga.owner, gb.id, ListMessagesInput{}); return err }(), "group_not_found"},
			{func() error { _, _, err := e.svc.OpenArtifact(ctx, ga.owner, gb.id, rids[b], artIDs[b]); return err }(), "group_not_found"},
			{func() error { _, _, err := e.svc.OpenArtifact(ctx, ga.owner, ga.id, rids[b], artIDs[b]); return err }(), "artifact_not_found"},
		}
		for i, c := range checks {
			if code(c.err) != c.want {
				t.Fatalf("g%d->g%d check %d: got %v want %s", a, b, i, c.err, c.want)
			}
		}
	}
	owners := e.ridOwners(t)
	for _, g := range e.groups {
		e.checkGroup(t, g, owners)
	}
}
