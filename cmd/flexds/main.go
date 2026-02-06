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
	"github.com/moonkev/flexds/internal/discovery"
	"github.com/moonkev/flexds/internal/discovery/consul"
	"github.com/moonkev/flexds/internal/discovery/yaml"
	"github.com/moonkev/flexds/internal/server"
	"github.com/moonkev/flexds/internal/xds"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultConsulAddr = "localhost:8500"
	defaultAdsPort    = 18000
	defaultAdminPort  = 19005
)

func main() {
	var adsPort int
	var adminPort int
	var debugLogging bool
	var consulDiscovery bool
	var consulAddr string
	var watcherStrategy string
	var yamlDiscovery bool
	var yamlFile string

	flag.IntVar(&adsPort, "ads-port", defaultAdsPort, "ADS gRPC port")
	flag.IntVar(&adminPort, "admin-port", defaultAdminPort, "admin port")
	flag.BoolVar(&debugLogging, "debug", false, "enable debug logging")
	flag.BoolVar(&consulDiscovery, "consul", false, "Use Consul for service discovery")
	flag.StringVar(&consulAddr, "consul-addr", defaultConsulAddr, "consul HTTP address (host:port)")
	flag.StringVar(&watcherStrategy, "consul-watcher-strategy", "immediate", "consul watcher strategy: immediate, debounce, or batch")
	flag.BoolVar(&yamlDiscovery, "yaml", false, "Use YAML file for service discovery")
	flag.StringVar(&yamlFile, "yaml-file", "", "path to YAML configuration file (required when discovery=yaml)")
	flag.Parse()

	// Validate flags
	if !consulDiscovery && !yamlDiscovery {
		slog.Error("at least one discovery mode must be enabled: -consul or -yaml")
		os.Exit(1)
	}

	if yamlDiscovery && yamlFile == "" {
		slog.Error("yaml-file must be specified when using yaml discovery mode")
		os.Exit(1)
	}

	// Configure structured logging
	var level = slog.LevelInfo
	if debugLogging {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Initialize metrics
	server.InitMetrics()

	slog.Info("starting control plane with blocking queries", "consul", consulAddr)

	// Create snapshot cache
	snapshotCache := cachev3.NewSnapshotCache(true, cachev3.IDHash{}, nil)
	snapshotManager := xds.NewSnapshotManager(snapshotCache)
	aggregator := discovery.NewDiscoveredServiceAggregator(snapshotManager)

	// Create XDS server
	slog.Info("creating XDS server")
	callbacks := &server.ServerCallbacks{Cache: snapshotCache}
	adsServer := serverv3.NewServer(context.Background(), snapshotCache, callbacks)
	slog.Info("XDS server created")

	// Setup context and channels
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.RunGRPC(ctx, adsServer, adsPort)
	}()

	// Setup admin/metrics HTTP server
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
			consul.WatchConsulBlocking(ctx, consulAddr, consulConfig, aggregator)
		}()
	}

	if yamlDiscovery {
		yamlConfig := yaml.Config{ConfigPath: yamlFile}
		if err := yaml.LoadConfig(yamlConfig, aggregator); err != nil {
			slog.Error("failed to load YAML config", "error", err)
			os.Exit(1)
		}
	}

	// Wait for shutdown signal
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

	// Graceful shutdown of HTTP admin server
	shutdownCtx2, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	if err := admin.Shutdown(shutdownCtx2); err != nil {
		slog.Error("admin server shutdown error", "error", err)
	}

	slog.Info("exiting")
}
