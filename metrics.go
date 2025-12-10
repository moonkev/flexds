package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus metrics
var (
	metricSnapshotsPushed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "flexds_snapshots_pushed_total",
			Help: "Total number of snapshots pushed to the cache",
		},
	)
	metricServicesDiscovered = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "flexds_services_discovered",
			Help: "Number of services discovered from Consul",
		},
	)
)

// InitMetrics registers Prometheus metrics
func InitMetrics() {
	prometheus.MustRegister(metricSnapshotsPushed)
	prometheus.MustRegister(metricServicesDiscovered)
}
