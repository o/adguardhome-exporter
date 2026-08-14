package exporter

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/o/adguardhome-exporter/internal/adguard"
	"github.com/o/adguardhome-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestExporterUsesStatusForUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/control/status" {
			_, _ = response.Write([]byte(`{"version":"v0.107.78","running":true}`))
			return
		}

		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	metrics := gatherMetrics(t, server.URL)

	if value := metricValue(t, metrics, "adguardhome_up", nil); value != 1 {
		t.Errorf("adguardhome_up = %v, want 1", value)
	}
	if value := metricValue(t, metrics, "adguardhome_collector_success", map[string]string{"collector": "dhcp"}); value != 0 {
		t.Errorf("DHCP collector success = %v, want 0", value)
	}
}

func TestExporterSanitizesUpstreamLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/control/status":
			_, _ = response.Write([]byte(`{"version":"v0.107.78","running":true}`))
		case "/control/stats":
			_, _ = response.Write([]byte(`{
				"top_upstreams_responses":[{"https://user:password@dns.example/dns-query?token=secret":12}],
				"top_upstreams_avg_time":[]
			}`))
		case "/control/stats/config":
			_, _ = response.Write([]byte(`{"enabled":true,"interval":3600000}`))
		case "/control/filtering/status":
			_, _ = response.Write([]byte(`{}`))
		case "/control/dhcp/status":
			_, _ = response.Write([]byte(`{}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	metrics := gatherMetrics(t, server.URL)
	labels := map[string]string{"upstream": "https://dns.example/dns-query"}
	if value := metricValue(t, metrics, "adguardhome_upstream_responses", labels); value != 12 {
		t.Errorf("sanitized upstream value = %v, want 12", value)
	}
	if value := counterValue(t, metrics, "adguardhome_dns_queries_total", nil); value != 0 {
		t.Errorf("initial DNS query counter = %v, want 0", value)
	}
}

func TestStatisticsTotals(t *testing.T) {
	exporter := &Exporter{statisticsTotals: make(map[string]statisticsTotals)}

	tests := []struct {
		queries     float64
		blocked     float64
		wantQueries float64
		wantBlocked float64
	}{
		{queries: 100, blocked: 20, wantQueries: 0, wantBlocked: 0},
		{queries: 125, blocked: 25, wantQueries: 25, wantBlocked: 5},
		{queries: 4, blocked: 1, wantQueries: 29, wantBlocked: 6},
		{queries: 9, blocked: 2, wantQueries: 34, wantBlocked: 7},
	}

	for _, test := range tests {
		totals := exporter.updateStatisticsTotals("home", adguard.Statistics{
			NumDNSQueries:       test.queries,
			NumBlockedFiltering: test.blocked,
		})
		if totals.queriesTotal != test.wantQueries {
			t.Errorf("query total = %v, want %v", totals.queriesTotal, test.wantQueries)
		}
		if totals.blockedTotal != test.wantBlocked {
			t.Errorf("blocked total = %v, want %v", totals.blockedTotal, test.wantBlocked)
		}
	}
}

func gatherMetrics(t *testing.T, targetURL string) []*dto.MetricFamily {
	t.Helper()

	cfg := config.Default()
	cfg.Targets = []config.Target{{Name: "home", URL: targetURL}}
	cfg.Web.Timeout = time.Second

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	exporter, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)

	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	return metrics
}

func metricValue(t *testing.T, metrics []*dto.MetricFamily, name string, expectedLabels map[string]string) float64 {
	t.Helper()

	for _, family := range metrics {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if hasLabels(metric, expectedLabels) {
				return metric.GetGauge().GetValue()
			}
		}
	}

	t.Fatalf("metric %q with labels %v was not found", name, expectedLabels)
	return 0
}

func counterValue(t *testing.T, metrics []*dto.MetricFamily, name string, expectedLabels map[string]string) float64 {
	t.Helper()

	for _, family := range metrics {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if hasLabels(metric, expectedLabels) {
				return metric.GetCounter().GetValue()
			}
		}
	}

	t.Fatalf("counter %q with labels %v was not found", name, expectedLabels)
	return 0
}

func hasLabels(metric *dto.Metric, expected map[string]string) bool {
	for key, value := range expected {
		found := false
		for _, label := range metric.Label {
			if label.GetName() == key && label.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
