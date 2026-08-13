package adguard

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestCleanLabelRemovesCredentialsAndQuery(t *testing.T) {
	value := CleanLabel("https://user:password@dns.example/dns-query?token=secret#fragment")
	if value != "https://dns.example/dns-query" {
		t.Errorf("CleanLabel = %q", value)
	}
}
