package group

import (
	"context"
	"log"
	"time"
)

// The proposal worker (P6 §4 of the plan): a single background loop per
// process that drains the persistent job queue. Jobs are claimed with a
// lease, so a crashed process never strands a requirement —
// requeueExpired (run every tick) returns lapsed jobs to the queue, and
// "proposing" is a resumable requirement state. Multiple server processes
// may run workers concurrently: claiming is a CAS in the store and
// SerialKey keeps at most one running job per (group, assignee).

const (
	jobLeaseDuration = 2 * time.Minute
	workerPoll       = 500 * time.Millisecond
)

type ProposalWorker struct {
	svc  *Service
	id   string
	poll time.Duration
}

func NewProposalWorker(svc *Service, id string) *ProposalWorker {
	if id == "" {
		id = newID("worker_")
	}
	return &ProposalWorker{svc: svc, id: id, poll: workerPoll}
}

// Run blocks until ctx is cancelled. Start it from the transport wiring
// with the process shutdown context.
func (w *ProposalWorker) Run(ctx context.Context) {
	for {
		if err := w.svc.requeueExpired(ctx); err != nil && ctx.Err() == nil {
			log.Printf("group worker: requeue expired: %v", err)
		}
		for {
			job, err := w.svc.claimNext(ctx, w.id)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("group worker: claim: %v", err)
				}
				break
			}
			if job == nil {
				break
			}
			if err := w.svc.runProposalJob(ctx, w.id, job); err != nil && ctx.Err() == nil {
				log.Printf("group worker: job %s: %v", job.ID, err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.poll):
		}
	}
}
