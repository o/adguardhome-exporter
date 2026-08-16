package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/o/adguardhome-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHandlerEndpoints(t *testing.T) {
	handler := newHandler(prometheus.NewRegistry())

	for _, path := range []string{"/metrics", "/-/healthy", "/-/ready"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
			}
		})
	}

	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Errorf("missing route status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestNewLogger(t *testing.T) {
	debugLogger := newLogger(config.LogConfig{Level: "debug", Format: "text"})
	if !debugLogger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug logger does not enable debug messages")
	}

	jsonLogger := newLogger(config.LogConfig{Level: "error", Format: "json"})
	if jsonLogger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("error logger enables info messages")
	}
}

func TestCheckHealth(t *testing.T) {
	healthyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer healthyServer.Close()

	if err := checkHealth(healthyServer.URL); err != nil {
		t.Fatalf("healthy server returned an error: %v", err)
	}

	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthyServer.Close()

	if err := checkHealth(unhealthyServer.URL); err == nil {
		t.Fatal("unhealthy server returned no error")
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := map[string]string{
		"debug": "DEBUG",
		"info":  "INFO",
		"warn":  "WARN",
		"error": "ERROR",
		"other": "INFO",
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := parseLogLevel(input).String(); got != want {
				t.Errorf("level = %s, want %s", got, want)
			}
		})
	}
}
