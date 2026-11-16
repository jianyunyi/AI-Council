package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGrafanaDashboardUsesExportedMetrics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "grafana", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Panels []struct {
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("invalid dashboard JSON: %v", err)
	}
	if len(dashboard.Panels) < 10 {
		t.Fatalf("dashboard has %d panels, want at least 10 operational and business panels", len(dashboard.Panels))
	}

	recorder := httptest.NewRecorder()
	New().Handler(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	available := make(map[string]bool)
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			available[strings.SplitN(fields[0], "{", 2)[0]] = true
		}
	}
	expressionMetric := regexp.MustCompile(`\baicouncil_[a-zA-Z0-9_]+`)
	used := make(map[string]bool)
	for _, panel := range dashboard.Panels {
		for _, target := range panel.Targets {
			for _, metric := range expressionMetric.FindAllString(target.Expr, -1) {
				used[metric] = true
				if !available[metric] {
					t.Errorf("dashboard query references metric not exported by Council: %s", metric)
				}
			}
		}
	}
	for _, required := range []string{
		"aicouncil_http_request_duration_seconds_bucket",
		"aicouncil_auth_failures_total",
		"aicouncil_rate_limited_total",
		"aicouncil_approvals_granted_total",
		"aicouncil_runner_execution_duration_seconds_bucket",
		"aicouncil_provider_estimated_cost_micros_by_provider_total",
		"aicouncil_sqlite_lock_contentions_total",
	} {
		if !used[required] {
			t.Errorf("dashboard does not visualize required metric %s", required)
		}
	}
}

func TestPrometheusScrapesCouncilInternally(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "monitoring", "prometheus.yml"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, required := range []string{"scrape_interval: 15s", "metrics_path: /metrics", "council:8080"} {
		if !strings.Contains(config, required) {
			t.Errorf("Prometheus config missing %q:\n%s", required, config)
		}
	}
}
