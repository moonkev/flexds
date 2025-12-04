package main

import (
"context"
"flag"
"fmt"
"log"
"net/http"
"os"
"os/signal"
"sync"
"syscall"
"time"

consulapi "github.com/hashicorp/consul/api"
cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
defaultConsulAddr = "localhost:8500"
defaultAdsPort    = 18000
defaultAdminPort  = 19005
)

// Config holds the application configuration
type Config struct {
	ConsulAddr  string
	ADSPort     int
	AdminPort   int
	WaitTimeSec int
}

func main() {
	var consulAddr string
	var adsPort int
	var adminPort int

	flag.StringVar(&consulAddr, "consul", defaultConsulAddr, "consul HTTP address (host:port)")
	flag.IntVar(&adsPort, "ads-port", defaultAdsPort, "ADS gRPC port")
	flag.IntVar(&adminPort, "admin-port", defaultAdminPort, "admin port")
	flag.Parse()

	// Initialize metrics
	InitMetrics()

	log.Printf("starting control plane with blocking queries; consul=%s", consulAddr)

	// Create Consul client
	consulCfg := consulapi.DefaultConfig()
	consulCfg.Address = fmt.Sprintf("http://%s", consulAddr)
	consulClient, err := consulapi.NewClient(consulCfg)
	if err != nil {
		log.Fatalf("failed to create consul client: %v", err)
	}

	// Create snapshot cache
	snapshotCache := cachev3.NewSnapshotCache(true, cachev3.IDHash{}, nil)

	// Create XDS server
	log.Printf("creating XDS server...")
	callbacks := &ServerCallbacks{}
	adsServer := serverv3.NewServer(context.Background(), snapshotCache, callbacks)
	log.Printf("XDS server created")

	// Setup context and channels
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		RunGRPC(ctx, adsServer, adsPort)
	}()

	// Setup admin/metrics HTTP server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	admin := &http.Server{Addr: fmt.Sprintf(":%d", adminPort), Handler: mux}
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("starting admin http on :%d", adminPort)
		if err := admin.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("admin server failed: %v", err)
		}
	}()

	// Start Consul watch
	config := &Config{
		ConsulAddr:  consulAddr,
		ADSPort:     adsPort,
		AdminPort:   adminPort,
		WaitTimeSec: 10,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		WatchConsulBlocking(ctx, consulClient, snapshotCache, config)
	}()

	// Wait for shutdown signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	<-stop
	log.Printf("shutdown signal received, shutting down services...")
	cancel()

	// Wait for all goroutines with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	select {
	case <-done:
		log.Printf("all services stopped gracefully")
	case <-shutdownCtx.Done():
		log.Printf("shutdown timeout exceeded, forcing exit")
	}

	// Graceful shutdown of HTTP admin server
	shutdownCtx2, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	if err := admin.Shutdown(shutdownCtx2); err != nil {
		log.Printf("admin server shutdown error: %v", err)
	}

	log.Printf("exiting")
}
