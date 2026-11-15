package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerExposesBoundedOperationalAndBusinessMetrics(t *testing.T) {
	m := New()
	m.RecordHTTP(http.StatusCreated, 20*time.Millisecond)
	m.RecordHTTP(http.StatusServiceUnavailable, 2*time.Second)
	m.RecordAuthFailure()
	m.RecordPermissionDenial()
	m.RecordRateLimited()
	m.RecordProviderCall("openai", false, 2, 0)
	m.RecordProviderCall("openai", true, 0, 1200)
	m.RecordProviderCall("arbitrary-user-value", true, 0, 10)
	m.TasksCreated.Add(1)
	m.CouncilRuns.Add(1)
	m.ApprovalsGranted.Add(1)
	m.RecordRunnerExecution(false, 30*time.Millisecond)
	m.RecordSQLiteLockWait(35 * time.Millisecond)

	recorder := httptest.NewRecorder()
	m.Handler(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		"aicouncil_http_requests_total 2",
		`aicouncil_http_responses_total{status_class="2xx"} 1`,
		`aicouncil_http_responses_total{status_class="5xx"} 1`,
		"aicouncil_http_request_duration_seconds_count 2",
		`aicouncil_auth_failures_total 1`,
		`aicouncil_permission_denials_total 1`,
		`aicouncil_rate_limited_total 1`,
		`aicouncil_provider_calls_total 3`,
		`aicouncil_provider_errors_total 1`,
		`aicouncil_provider_retries_total 2`,
		`aicouncil_provider_estimated_cost_micros_total 1210`,
		`aicouncil_provider_calls_by_provider_total{provider="openai"} 2`,
		`aicouncil_provider_calls_by_provider_total{provider="other"} 1`,
		`aicouncil_tasks_created_total 1`,
		`aicouncil_council_runs_total 1`,
		`aicouncil_executions_total 1`,
		`aicouncil_approvals_granted_total 1`,
		`aicouncil_runner_execution_duration_seconds_count 1`,
		`aicouncil_runner_execution_failures_total 1`,
		`aicouncil_sqlite_lock_contentions_total 1`,
		`aicouncil_sqlite_lock_wait_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "arbitrary-user-value") {
		t.Fatalf("unbounded provider label leaked into metrics: %s", body)
	}
}
