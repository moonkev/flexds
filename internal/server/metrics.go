package server

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus metrics
var (
	MetricSnapshotsPushed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "flexds_snapshots_pushed_total",
			Help: "Total number of snapshots pushed to the cache",
		},
	)
	MetricServicesDiscovered = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "flexds_services_discovered",
			Help: "Number of services discovered from Consul",
		},
	)
)

// InitMetrics registers Prometheus metrics
func InitMetrics() {
	prometheus.MustRegister(MetricSnapshotsPushed)
	prometheus.MustRegister(MetricServicesDiscovered)
}
