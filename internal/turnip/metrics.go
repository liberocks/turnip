package turnip

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry          *prometheus.Registry
	AuthAttempts      *prometheus.CounterVec
	ConnectionsActive prometheus.Gauge
	ConnectionsTotal  prometheus.Counter
	Messages          *prometheus.CounterVec
	RoomEvents        *prometheus.CounterVec
	DroppedMessages   *prometheus.CounterVec
	RedisOperations   *prometheus.HistogramVec
	ReadinessFailures prometheus.Counter
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		Registry: registry,
		AuthAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "turnip", Name: "auth_attempts_total", Help: "Authentication attempts by bounded outcome.",
		}, []string{"result", "reason"}),
		ConnectionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "turnip", Name: "connections_active", Help: "WebSocket connections active on this instance.",
		}),
		ConnectionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "turnip", Name: "connections_total", Help: "WebSocket connections accepted on this instance.",
		}),
		Messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "turnip", Name: "messages_total", Help: "Protocol messages by direction and type.",
		}, []string{"direction", "type"}),
		RoomEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "turnip", Name: "room_events_total", Help: "Room lifecycle events by type.",
		}, []string{"type"}),
		DroppedMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "turnip", Name: "dropped_messages_total", Help: "Dropped outbound messages by bounded reason.",
		}, []string{"reason"}),
		RedisOperations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "turnip", Name: "redis_operation_duration_seconds", Help: "Redis operation duration by operation and result.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}, []string{"operation", "result"}),
		ReadinessFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "turnip", Name: "readiness_failures_total", Help: "Failed readiness checks.",
		}),
	}
	registry.MustRegister(
		metrics.AuthAttempts,
		metrics.ConnectionsActive,
		metrics.ConnectionsTotal,
		metrics.Messages,
		metrics.RoomEvents,
		metrics.DroppedMessages,
		metrics.RedisOperations,
		metrics.ReadinessFailures,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return metrics
}

func (m *Metrics) ObserveRedis(operation string, started time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.RedisOperations.WithLabelValues(operation, result).Observe(time.Since(started).Seconds())
}

func (m *Metrics) Handler(username, password string) http.Handler {
	handler := promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
	if username == "" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualUser, actualPassword, ok := r.BasicAuth()
		userMatch := subtle.ConstantTimeCompare([]byte(actualUser), []byte(username)) == 1
		passwordMatch := subtle.ConstantTimeCompare([]byte(actualPassword), []byte(password)) == 1
		if !ok || !userMatch || !passwordMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="Turnip Metrics"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
