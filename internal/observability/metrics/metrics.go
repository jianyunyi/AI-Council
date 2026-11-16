package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

var requestDurationBuckets = [...]time.Duration{
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2500 * time.Millisecond,
	5 * time.Second,
	10 * time.Second,
}

type Metrics struct {
	Requests, TasksCreated, Executions, ExecutionFailures  atomic.Uint64
	ApprovalsGranted, ApprovalsRejected                    atomic.Uint64
	RunnerExecutionCount, RunnerExecutionNanos             atomic.Uint64
	RunnerExecutionFailures                                atomic.Uint64
	RunnerExecutionBuckets                                 [len(requestDurationBuckets) + 1]atomic.Uint64
	CouncilRuns, CouncilFailures                           atomic.Uint64
	Responses2xx, Responses3xx, Responses4xx, Responses5xx atomic.Uint64
	RequestDurationCount, RequestDurationNanos             atomic.Uint64
	RequestDurationBuckets                                 [len(requestDurationBuckets) + 1]atomic.Uint64
	AuthFailures, PermissionDenials, RateLimited           atomic.Uint64
	ProviderCalls, ProviderErrors, ProviderRetries         atomic.Uint64
	ProviderEstimatedCostMicros                            atomic.Int64
	providers                                              [4]providerMetrics
	SQLiteLockContentions                                  atomic.Uint64
	SQLiteLockWaitCount, SQLiteLockWaitNanos               atomic.Uint64
	SQLiteLockWaitBuckets                                  [len(requestDurationBuckets) + 1]atomic.Uint64
}

type providerMetrics struct {
	calls, errors, retries atomic.Uint64
	costMicros             atomic.Int64
}

func New() *Metrics { return &Metrics{} }

func (m *Metrics) RecordHTTP(status int, duration time.Duration) {
	m.Requests.Add(1)
	switch {
	case status >= 200 && status < 300:
		m.Responses2xx.Add(1)
	case status >= 300 && status < 400:
		m.Responses3xx.Add(1)
	case status >= 400 && status < 500:
		m.Responses4xx.Add(1)
	case status >= 500:
		m.Responses5xx.Add(1)
	}
	if duration < 0 {
		duration = 0
	}
	m.RequestDurationCount.Add(1)
	m.RequestDurationNanos.Add(uint64(duration))
	for i, bound := range requestDurationBuckets {
		if duration <= bound {
			m.RequestDurationBuckets[i].Add(1)
		}
	}
	m.RequestDurationBuckets[len(requestDurationBuckets)].Add(1)
}

func (m *Metrics) RecordAuthFailure()      { m.AuthFailures.Add(1) }
func (m *Metrics) RecordPermissionDenial() { m.PermissionDenials.Add(1) }
func (m *Metrics) RecordRateLimited()      { m.RateLimited.Add(1) }

func (m *Metrics) RecordProviderCall(provider string, success bool, retries int, estimatedCostMicros int64) {
	perProvider := &m.providers[providerIndex(provider)]
	m.ProviderCalls.Add(1)
	perProvider.calls.Add(1)
	if !success {
		m.ProviderErrors.Add(1)
		perProvider.errors.Add(1)
	}
	if retries > 0 {
		m.ProviderRetries.Add(uint64(retries))
		perProvider.retries.Add(uint64(retries))
	}
	if success && estimatedCostMicros > 0 {
		m.ProviderEstimatedCostMicros.Add(estimatedCostMicros)
		perProvider.costMicros.Add(estimatedCostMicros)
	}
}

func (m *Metrics) RecordProviderRetry(provider string) {
	m.ProviderRetries.Add(1)
	m.providers[providerIndex(provider)].retries.Add(1)
}

func (m *Metrics) RecordRunnerExecution(success bool, duration time.Duration) {
	m.Executions.Add(1)
	if !success {
		m.ExecutionFailures.Add(1)
		m.RunnerExecutionFailures.Add(1)
	}
	if duration < 0 {
		duration = 0
	}
	m.RunnerExecutionCount.Add(1)
	m.RunnerExecutionNanos.Add(uint64(duration))
	for i, bound := range requestDurationBuckets {
		if duration <= bound {
			m.RunnerExecutionBuckets[i].Add(1)
		}
	}
	m.RunnerExecutionBuckets[len(requestDurationBuckets)].Add(1)
}

func (m *Metrics) RecordSQLiteLockWait(duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	m.SQLiteLockContentions.Add(1)
	m.SQLiteLockWaitCount.Add(1)
	m.SQLiteLockWaitNanos.Add(uint64(duration))
	for i, bound := range requestDurationBuckets {
		if duration <= bound {
			m.SQLiteLockWaitBuckets[i].Add(1)
		}
	}
	m.SQLiteLockWaitBuckets[len(requestDurationBuckets)].Add(1)
}

func providerIndex(name string) int {
	switch name {
	case "openai":
		return 0
	case "anthropic":
		return 1
	case "deepseek":
		return 2
	default:
		return 3
	}
}

func providerName(index int) string {
	return [...]string{"openai", "anthropic", "deepseek", "other"}[index]
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "aicouncil_http_requests_total %d\n", m.Requests.Load())
	fmt.Fprintf(w, "aicouncil_http_responses_total{status_class=\"2xx\"} %d\n", m.Responses2xx.Load())
	fmt.Fprintf(w, "aicouncil_http_responses_total{status_class=\"3xx\"} %d\n", m.Responses3xx.Load())
	fmt.Fprintf(w, "aicouncil_http_responses_total{status_class=\"4xx\"} %d\n", m.Responses4xx.Load())
	fmt.Fprintf(w, "aicouncil_http_responses_total{status_class=\"5xx\"} %d\n", m.Responses5xx.Load())
	for i, bound := range requestDurationBuckets {
		fmt.Fprintf(w, "aicouncil_http_request_duration_seconds_bucket{le=\"%s\"} %d\n", strconv.FormatFloat(bound.Seconds(), 'f', -1, 64), m.RequestDurationBuckets[i].Load())
	}
	fmt.Fprintf(w, "aicouncil_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", m.RequestDurationBuckets[len(requestDurationBuckets)].Load())
	fmt.Fprintf(w, "aicouncil_http_request_duration_seconds_sum %.9f\n", float64(m.RequestDurationNanos.Load())/float64(time.Second))
	fmt.Fprintf(w, "aicouncil_http_request_duration_seconds_count %d\n", m.RequestDurationCount.Load())
	fmt.Fprintf(w, "aicouncil_auth_failures_total %d\n", m.AuthFailures.Load())
	fmt.Fprintf(w, "aicouncil_permission_denials_total %d\n", m.PermissionDenials.Load())
	fmt.Fprintf(w, "aicouncil_rate_limited_total %d\n", m.RateLimited.Load())
	fmt.Fprintf(w, "aicouncil_tasks_created_total %d\n", m.TasksCreated.Load())
	fmt.Fprintf(w, "aicouncil_council_runs_total %d\n", m.CouncilRuns.Load())
	fmt.Fprintf(w, "aicouncil_council_failures_total %d\n", m.CouncilFailures.Load())
	fmt.Fprintf(w, "aicouncil_executions_total %d\n", m.Executions.Load())
	fmt.Fprintf(w, "aicouncil_execution_failures_total %d\n", m.ExecutionFailures.Load())
	fmt.Fprintf(w, "aicouncil_approvals_granted_total %d\n", m.ApprovalsGranted.Load())
	fmt.Fprintf(w, "aicouncil_approvals_rejected_total %d\n", m.ApprovalsRejected.Load())
	for i, bound := range requestDurationBuckets {
		fmt.Fprintf(w, "aicouncil_runner_execution_duration_seconds_bucket{le=\"%s\"} %d\n", strconv.FormatFloat(bound.Seconds(), 'f', -1, 64), m.RunnerExecutionBuckets[i].Load())
	}
	fmt.Fprintf(w, "aicouncil_runner_execution_duration_seconds_bucket{le=\"+Inf\"} %d\n", m.RunnerExecutionBuckets[len(requestDurationBuckets)].Load())
	fmt.Fprintf(w, "aicouncil_runner_execution_duration_seconds_sum %.9f\n", float64(m.RunnerExecutionNanos.Load())/float64(time.Second))
	fmt.Fprintf(w, "aicouncil_runner_execution_duration_seconds_count %d\n", m.RunnerExecutionCount.Load())
	fmt.Fprintf(w, "aicouncil_runner_execution_failures_total %d\n", m.RunnerExecutionFailures.Load())
	fmt.Fprintf(w, "aicouncil_sqlite_lock_contentions_total %d\n", m.SQLiteLockContentions.Load())
	for i, bound := range requestDurationBuckets {
		fmt.Fprintf(w, "aicouncil_sqlite_lock_wait_seconds_bucket{le=\"%s\"} %d\n", strconv.FormatFloat(bound.Seconds(), 'f', -1, 64), m.SQLiteLockWaitBuckets[i].Load())
	}
	fmt.Fprintf(w, "aicouncil_sqlite_lock_wait_seconds_bucket{le=\"+Inf\"} %d\n", m.SQLiteLockWaitBuckets[len(requestDurationBuckets)].Load())
	fmt.Fprintf(w, "aicouncil_sqlite_lock_wait_seconds_sum %.9f\n", float64(m.SQLiteLockWaitNanos.Load())/float64(time.Second))
	fmt.Fprintf(w, "aicouncil_sqlite_lock_wait_seconds_count %d\n", m.SQLiteLockWaitCount.Load())
	fmt.Fprintf(w, "aicouncil_provider_calls_total %d\n", m.ProviderCalls.Load())
	fmt.Fprintf(w, "aicouncil_provider_errors_total %d\n", m.ProviderErrors.Load())
	fmt.Fprintf(w, "aicouncil_provider_retries_total %d\n", m.ProviderRetries.Load())
	fmt.Fprintf(w, "aicouncil_provider_estimated_cost_micros_total %d\n", m.ProviderEstimatedCostMicros.Load())
	for i := range m.providers {
		provider := &m.providers[i]
		name := providerName(i)
		fmt.Fprintf(w, "aicouncil_provider_calls_by_provider_total{provider=\"%s\"} %d\n", name, provider.calls.Load())
		fmt.Fprintf(w, "aicouncil_provider_errors_by_provider_total{provider=\"%s\"} %d\n", name, provider.errors.Load())
		fmt.Fprintf(w, "aicouncil_provider_retries_by_provider_total{provider=\"%s\"} %d\n", name, provider.retries.Load())
		fmt.Fprintf(w, "aicouncil_provider_estimated_cost_micros_by_provider_total{provider=\"%s\"} %d\n", name, provider.costMicros.Load())
	}
}
