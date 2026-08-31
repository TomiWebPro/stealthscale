package hscontrol

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"tailscale.com/envknob"
)

var debugHighCardinalityMetrics = envknob.Bool("STEALTHSCALE_DEBUG_HIGH_CARDINALITY_METRICS")

var mapResponseLastSentSeconds *prometheus.GaugeVec

func init() {
	if debugHighCardinalityMetrics {
		mapResponseLastSentSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: prometheusNamespace,
			Name:      "mapresponse_last_sent_seconds",
			Help:      "last sent metric to node.id",
		}, []string{"type", "id"})
	}
}

const prometheusNamespace = "stealthscale"

var (
	mapResponseSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: prometheusNamespace,
		Name:      "mapresponse_sent_total",
		Help:      "total count of mapresponses sent to clients",
	}, []string{"status", "type"})
	mapResponseEndpointUpdates = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: prometheusNamespace,
		Name:      "mapresponse_endpoint_updates_total",
		Help:      "total count of endpoint updates received",
	}, []string{"status"})
	mapResponseEnded = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: prometheusNamespace,
		Name:      "mapresponse_ended_total",
		Help:      "total count of new mapsessions ended",
	}, []string{"reason"})

	derpGatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: prometheusNamespace,
		Name:      "derp_gated_total",
		Help:      "total count of DERP gate suppressions (fail-closed when stealth not satisfied)",
	})
	stealthReadyGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: prometheusNamespace,
		Name:      "stealth_ready",
		Help:      "whether stealth transport is ready (1) or not (0)",
	})
)

// SetStealthReady sets the stealth_ready gauge (1 for ready, 0 for not).
func SetStealthReady(ready bool) {
	if ready {
		stealthReadyGauge.Set(1)
	} else {
		stealthReadyGauge.Set(0)
	}
}

// IncDERPGated increments the derp_gated_total counter.
func IncDERPGated() {
	derpGatedTotal.Inc()
}
