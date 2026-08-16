package exporter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/o/adguardhome-exporter/internal/adguard"
	"github.com/o/adguardhome-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestExporterBaselinesStatisticsCounters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/control/status":
			_, _ = response.Write([]byte(`{"version":"v0.107.78","running":true}`))
		case "/control/stats":
			_, _ = response.Write([]byte(`{"num_dns_queries":1000,"num_blocked_filtering":250}`))
		case "/control/stats/config":
			_, _ = response.Write([]byte(`{"enabled":true,"interval":3600000}`))
		case "/control/filtering/status", "/control/dhcp/status":
			_, _ = response.Write([]byte(`{}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	metrics := gatherMetrics(t, server.URL)
	if got := metricValue(t, metrics, "adguardhome_dns_queries", nil); got != 1000 {
		t.Errorf("DNS queries = %v, want 1000", got)
	}
	if got := counterValue(t, metrics, "adguardhome_dns_queries_total", nil); got != 0 {
		t.Errorf("initial DNS query counter = %v, want 0", got)
	}
	if metricExists(metrics, "adguardhome_upstream_responses_total") {
		t.Error("upstream response counter must not be exposed")
	}
}

func TestExporterServesStoredSnapshot(t *testing.T) {
	requests := make(map[string]int)
	var requestsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestsMu.Lock()
		requests[request.URL.Path]++
		requestsMu.Unlock()

		switch request.URL.Path {
		case "/control/status":
			_, _ = response.Write([]byte(`{"version":"v0.107.78"}`))
		case "/control/stats", "/control/stats/config", "/control/filtering/status", "/control/dhcp/status":
			_, _ = response.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Targets = []config.Target{{Name: "home", URL: server.URL}}
	exporter, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exporter.Start(ctx)

	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)
	for range 2 {
		if _, err := registry.Gather(); err != nil {
			t.Fatal(err)
		}
	}

	requestsMu.Lock()
	defer requestsMu.Unlock()
	for _, path := range []string{"/control/status", "/control/stats", "/control/stats/config", "/control/filtering/status", "/control/dhcp/status"} {
		if requests[path] != 1 {
			t.Errorf("requests to %s = %d, want 1", path, requests[path])
		}
	}
}

func TestExporterReportsUnavailableBeforeFirstPoll(t *testing.T) {
	cfg := config.Default()
	cfg.Targets = []config.Target{{Name: "home", URL: "http://adguard.example"}}
	exporter, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if got := metricValue(t, metrics, "adguardhome_up", nil); got != 0 {
		t.Errorf("up before first poll = %v, want 0", got)
	}
	if got := metricValue(t, metrics, "adguardhome_collector_success", map[string]string{"collector": "statistics"}); got != 0 {
		t.Errorf("statistics collector before first poll = %v, want 0", got)
	}
}

func TestExporterRecoversAfterFailedPoll(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if fail.Load() {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeTestResponse(response, request.URL.Path, 10)
	}))
	defer server.Close()

	registry, cancel := startTestExporter(t, server.URL, 5*time.Millisecond)
	defer cancel()
	fail.Store(false)
	eventually(t, func() bool {
		metrics, err := registry.Gather()
		return err == nil && metricValueOrZero(metrics, "adguardhome_up") == 1
	})
}

func TestExporterPollerUpdatesCounters(t *testing.T) {
	var queries atomic.Int64
	queries.Store(10)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeTestResponse(response, request.URL.Path, queries.Load())
	}))
	defer server.Close()

	registry, cancel := startTestExporter(t, server.URL, 5*time.Millisecond)
	defer cancel()
	queries.Store(20)
	eventually(t, func() bool {
		metrics, err := registry.Gather()
		return err == nil && metricValueOrZero(metrics, "adguardhome_dns_queries_total") == 10
	})
}

func TestExporterPollerStopsWhenContextIsCancelled(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writeTestResponse(response, request.URL.Path, 10)
	}))
	defer server.Close()

	_, cancel := startTestExporter(t, server.URL, 5*time.Millisecond)
	eventually(t, func() bool { return requests.Load() > 5 })
	cancel()
	time.Sleep(20 * time.Millisecond)
	before := requests.Load()
	time.Sleep(20 * time.Millisecond)
	if got := requests.Load(); got != before {
		t.Errorf("requests after cancellation = %d, want %d", got, before)
	}
}

func startTestExporter(t *testing.T, targetURL string, pollInterval time.Duration) (*prometheus.Registry, context.CancelFunc) {
	t.Helper()
	cfg := config.Default()
	cfg.Targets = []config.Target{{Name: "home", URL: targetURL}}
	cfg.Collectors.PollInterval = pollInterval
	exporter, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exporter.Start(ctx)
	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)
	return registry, cancel
}

func writeTestResponse(response http.ResponseWriter, path string, queries int64) {
	switch path {
	case "/control/status":
		_, _ = response.Write([]byte(`{"version":"v0.107.78"}`))
	case "/control/stats":
		_, _ = response.Write([]byte(fmt.Sprintf(`{"num_dns_queries":%d}`, queries)))
	case "/control/stats/config", "/control/filtering/status", "/control/dhcp/status":
		_, _ = response.Write([]byte(`{}`))
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestStatisticsCountersIgnoreWindowResets(t *testing.T) {
	var totals statisticsTotals

	tests := []struct {
		queries     float64
		blocked     float64
		wantQueries float64
		wantBlocked float64
	}{
		{queries: 100, blocked: 20, wantQueries: 0, wantBlocked: 0},
		{queries: 125, blocked: 25, wantQueries: 25, wantBlocked: 5},
		{queries: 4, blocked: 1, wantQueries: 25, wantBlocked: 5},
		{queries: 9, blocked: 2, wantQueries: 30, wantBlocked: 6},
	}

	for _, test := range tests {
		totals = updateStatisticsTotals(totals, adguard.Statistics{
			NumDNSQueries:       test.queries,
			NumBlockedFiltering: test.blocked,
		})
		if totals.queriesTotal != test.wantQueries {
			t.Errorf("query counter = %v, want %v", totals.queriesTotal, test.wantQueries)
		}
		if totals.blockedTotal != test.wantBlocked {
			t.Errorf("blocked query counter = %v, want %v", totals.blockedTotal, test.wantBlocked)
		}
	}
}

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
}

func TestExporterSanitizesUpstreamLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/control/status":
			_, _ = response.Write([]byte(`{"version":"v0.107.78","running":true}`))
		case "/control/stats":
			_, _ = response.Write([]byte(`{"top_upstreams_responses":[{"https://user:password@dns.example/dns-query?token=secret":12}]}`))
		case "/control/stats/config":
			_, _ = response.Write([]byte(`{"enabled":true,"interval":3600000}`))
		case "/control/filtering/status", "/control/dhcp/status":
			_, _ = response.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	metrics := gatherMetrics(t, server.URL)
	labels := map[string]string{"upstream": "https://dns.example/dns-query"}
	if value := metricValue(t, metrics, "adguardhome_upstream_responses", labels); value != 12 {
		t.Errorf("sanitized upstream value = %v, want 12", value)
	}
}

func TestUpMetricHelp(t *testing.T) {
	descriptor := newDescriptors().up.String()
	const want = `help: "Whether the AdGuard Home status API request succeeded."`
	if !strings.Contains(descriptor, want) {
		t.Errorf("adguardhome_up descriptor = %q, want it to contain %q", descriptor, want)
	}
}

func gatherMetrics(t *testing.T, targetURL string) []*dto.MetricFamily {
	t.Helper()
	cfg := config.Default()
	cfg.Targets = []config.Target{{Name: "home", URL: targetURL}}
	cfg.Web.Timeout = time.Second

	exporter, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exporter.Start(ctx)
	defer cancel()
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

func metricValueOrZero(metrics []*dto.MetricFamily, name string) float64 {
	for _, family := range metrics {
		if family.GetName() != name || len(family.Metric) == 0 {
			continue
		}
		if family.GetType() == dto.MetricType_COUNTER {
			return family.Metric[0].GetCounter().GetValue()
		}
		return family.Metric[0].GetGauge().GetValue()
	}
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

func metricExists(metrics []*dto.MetricFamily, name string) bool {
	for _, family := range metrics {
		if family.GetName() == name {
			return true
		}
	}
	return false
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
