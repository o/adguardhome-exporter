package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/o/adguardhome-exporter/internal/config"
	"github.com/o/adguardhome-exporter/internal/exporter"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	version  = "dev"
	revision = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
		return
	}

	configFile := flag.String("config.file", "", "Path to the YAML configuration file")
	showVersion := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("adguardhome-exporter version=%s revision=%s\n", version, revision)
		return
	}

	cfg, err := config.Load(*configFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Log)
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "adguardhome_exporter",
		Name:      "build_info",
		Help:      "Build information for the exporter.",
		ConstLabels: prometheus.Labels{
			"version":  version,
			"revision": revision,
		},
	}, func() float64 { return 1 }))

	exporter, err := exporter.New(cfg, logger)
	if err != nil {
		logger.Error("create exporter", "error", err)
		os.Exit(1)
	}
	registry.MustRegister(exporter)
	pollContext, cancelPolls := context.WithCancel(context.Background())
	defer cancelPolls()
	exporter.Start(pollContext)

	server := &http.Server{
		Addr:              cfg.Web.ListenAddress,
		Handler:           newHandler(registry),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("starting exporter", "address", cfg.Web.ListenAddress, "targets", len(cfg.Targets))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	<-shutdown
	cancelPolls()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown server", "error", err)
	}
}

func healthy(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusOK)
}

func newHandler(registry *prometheus.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/-/healthy", healthy)
	mux.HandleFunc("/-/ready", healthy)
	return mux
}

func healthcheck() {
	address := os.Getenv("ADGUARD_EXPORTER_HEALTHCHECK_ADDRESS")
	if address == "" {
		address = "http://127.0.0.1:9618/-/healthy"
	}

	if err := checkHealth(address); err != nil {
		os.Exit(1)
	}
}

func checkHealth(address string) error {
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %s", response.Status)
	}

	return nil
}

func newLogger(logConfig config.LogConfig) *slog.Logger {
	options := &slog.HandlerOptions{Level: parseLogLevel(logConfig.Level)}
	if logConfig.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, options))
	}

	return slog.New(slog.NewTextHandler(os.Stderr, options))
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
