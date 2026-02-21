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
	"sync"
	"syscall"
	"time"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/moonkev/flexds/internal/common/config"
	"github.com/moonkev/flexds/internal/common/telemetry"
	"github.com/moonkev/flexds/internal/discovery"
	"github.com/moonkev/flexds/internal/discovery/consul"
	"github.com/moonkev/flexds/internal/discovery/marathon"
	"github.com/moonkev/flexds/internal/discovery/yaml"
	"github.com/moonkev/flexds/internal/xds"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {

	var adsPort = 18000
	var adminPort = 19005
	var logLevel = config.LogLevelFlag(slog.LevelInfo)
	var consulDiscovery = false
	var consulAddr = "http://localhost:8500"
	var watcherStrategy = "immediate"
	var yamlDiscovery = false
	var yamlFile = ""
	var marathonDiscovery = false
	var marathonAddr = "http://localhost:8080"
	var marathonCredsPath = ""
	var marathonPollInterval = 30 * time.Second
	var listenerPorts config.Uint32SliceFlag = []uint32{18080}

	flag.IntVar(&adsPort, "ads-port", adsPort, "ADS gRPC port")
	flag.IntVar(&adminPort, "admin-port", adminPort, "admin port")
	flag.Var(&logLevel, "log-level", "log level: debug, info, warn, error (default: info)")
	flag.BoolVar(&consulDiscovery, "consul", false, "Use Consul for service discovery")
	flag.StringVar(&consulAddr, "consul-addr", consulAddr, "consul HTTP address (host:port)")
	flag.StringVar(&watcherStrategy, "consul-watcher-strategy", watcherStrategy, "consul watcher strategy: immediate, debounce, or batch")
	flag.BoolVar(&yamlDiscovery, "yaml", false, "Use YAML file for service discovery")
	flag.StringVar(&yamlFile, "yaml-file", "", "path to YAML configuration file (required when discovery=yaml)")
	flag.BoolVar(&marathonDiscovery, "marathon", false, "Use Marathon for service discovery")
	flag.StringVar(&marathonAddr, "marathon-addr", marathonAddr, "marathon HTTP address")
	flag.StringVar(&marathonCredsPath, "marathon-creds-path", "", "path to file containing marathon credentials (username:password)")
	flag.DurationVar(&marathonPollInterval, "marathon-poll-interval", marathonPollInterval, "interval between marathon service polls (default: 30s)")
	flag.Var(&listenerPorts, "listener-ports", "comma-separated list of listener ports (default: 18080)")
	flag.Parse()

	// Validate flags
	if !consulDiscovery && !yamlDiscovery && !marathonDiscovery {
		slog.Error("at least one discovery mode must be enabled: -consul|-yaml|-marathon")
		os.Exit(1)
	}

	if yamlDiscovery && yamlFile == "" {
		slog.Error("yaml-file must be specified when using yaml discovery mode")
		os.Exit(1)
	}

	if marathonDiscovery && marathonAddr == "" {
		slog.Error("marathon-addr must be specified when using marathon discovery mode")
		os.Exit(1)
	}

	// Configure structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel.Level()}))
	slog.SetDefault(logger)

	// Initialize metrics
	telemetry.InitMetrics()

	// Create snapshot cache
	snapshotCache := cachev3.NewSnapshotCache(true, cachev3.IDHash{}, nil)
	xdsConfig := xds.Config{
		Cache:         snapshotCache,
		ListenerPorts: listenerPorts,
	}
	snapshotManager := xds.NewSnapshotManager(xdsConfig)
	aggregator := discovery.NewDiscoveredServiceAggregator(snapshotManager)

	// Create XDS server
	slog.Info("creating XDS server")
	callbacks := &xds.ServerCallbacks{Cache: snapshotCache}
	adsServer := serverv3.NewServer(context.Background(), snapshotCache, callbacks)
	slog.Info("XDS server created")

	// Set up context and channels
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		xds.RunGRPC(ctx, adsServer, adsPort)
	}()

	// Set up admin/metrics HTTP server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	admin := &http.Server{Addr: fmt.Sprintf(":%d", adminPort), Handler: mux}
	wg.Add(1)
	go func() {
		defer wg.Done()
		slog.Info("starting admin http server", "port", adminPort)
		if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server failed", "error", err)
			os.Exit(1)
		}
	}()

	if consulDiscovery {
		consulConfig := &consul.Config{
			ConsulAddr:      consulAddr,
			WaitTimeSec:     2,
			WatcherStrategy: watcherStrategy,
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			consul.StartWatcher(ctx, consulAddr, consulConfig, aggregator)
		}()
	}

	if yamlDiscovery {
		yamlConfig := yaml.Config{ConfigPath: yamlFile}
		if err := yaml.LoadConfig(yamlConfig, aggregator); err != nil {
			slog.Error("failed to load YAML config", "error", err)
			os.Exit(1)
		}
	}

	if marathonDiscovery {
		marathonConfig := marathon.Config{
			URL:                 marathonAddr,
			CredentialsFilePath: marathonCredsPath,
			Interval:            marathonPollInterval,
		}
		if err := marathon.LoadConfig(ctx, marathonConfig, aggregator); err != nil {
			slog.Error("failed to load marathon config", "error", err)
			os.Exit(1)
		}
	}

	// Wait for a shutdown signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	<-stop
	slog.Info("shutdown signal received, shutting down services")
	cancel()

	// Wait for all goroutines with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	select {
	case <-done:
		slog.Info("all services stopped gracefully")
	case <-shutdownCtx.Done():
		slog.Warn("shutdown timeout exceeded, forcing exit")
	}

	// Graceful shutdown of the HTTP admin server
	shutdownCtx2, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	if err := admin.Shutdown(shutdownCtx2); err != nil {
		slog.Error("admin server shutdown error", "error", err)
	}

	slog.Info("exiting")
}
