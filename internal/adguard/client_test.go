package adguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/o/adguardhome-exporter/internal/config"
)

func TestStatusSendsBasicAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path != "/control/status" {
			t.Errorf("Path = %q, want /control/status", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{"version":"v0.107.78","running":true}`))
	}))
	defer server.Close()

	client, err := NewClient(config.Target{URL: server.URL, Username: "admin", Password: "secret"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Version != "v0.107.78" || !status.Running {
		t.Errorf("unexpected status: %#v", status)
	}
}

func TestCollectCallsEveryEndpoint(t *testing.T) {
	responses := map[string]string{
		"/control/status":           `{"version":"v0.107.78","running":true}`,
		"/control/stats":            `{"num_dns_queries":12}`,
		"/control/stats/config":     `{"enabled":true,"interval":3600000}`,
		"/control/filtering/status": `{"enabled":true}`,
		"/control/dhcp/status":      `{"enabled":false}`,
	}
	requests := make(map[string]int)
	var requestsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, ok := responses[request.URL.Path]
		if !ok {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		requestsMu.Lock()
		requests[request.URL.Path]++
		requestsMu.Unlock()
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()

	client, err := NewClient(config.Target{URL: server.URL}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	collection := client.Collect(context.Background())
	for path := range responses {
		requestsMu.Lock()
		count := requests[path]
		requestsMu.Unlock()
		if count != 1 {
			t.Errorf("requests to %s = %d, want 1", path, count)
		}
	}
	for _, result := range collection.Results() {
		if result.Err != nil {
			t.Errorf("collector %s failed: %v", result.Name, result.Err)
		}
	}
	if collection.Statistics.NumDNSQueries != 12 {
		t.Errorf("DNS queries = %v, want 12", collection.Statistics.NumDNSQueries)
	}
}

func TestStatusRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{name: "HTTP error", code: http.StatusInternalServerError},
		{name: "invalid JSON", body: `{`, code: http.StatusOK},
		{name: "response exceeds limit", body: `{"value":"` + strings.Repeat("x", maxResponseSize) + `"}`, code: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.code)
				_, _ = response.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := NewClient(config.Target{URL: server.URL}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Status(context.Background()); err == nil {
				t.Fatal("Status returned no error")
			}
		})
	}
}

func TestCleanLabelRemovesCredentialsAndQuery(t *testing.T) {
	value := CleanLabel("https://user:password@dns.example/dns-query?token=secret#fragment")
	if value != "https://dns.example/dns-query" {
		t.Errorf("CleanLabel = %q", value)
	}
}
