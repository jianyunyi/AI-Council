package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type Metrics struct {
	Requests, TasksCreated, Executions, ExecutionFailures atomic.Uint64
	CouncilRuns, CouncilFailures                          atomic.Uint64
}

func New() *Metrics { return &Metrics{} }
func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "aicouncil_http_requests_total %d\naicouncil_tasks_created_total %d\naicouncil_executions_total %d\naicouncil_execution_failures_total %d\naicouncil_council_runs_total %d\naicouncil_council_failures_total %d\n", m.Requests.Load(), m.TasksCreated.Load(), m.Executions.Load(), m.ExecutionFailures.Load(), m.CouncilRuns.Load(), m.CouncilFailures.Load())
}
