package main

import (
	"crypto/subtle"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

// Metrics holds all Prometheus metrics for the turnip signaling server
type Metrics struct {
	// Authentication metrics
	AuthAttempts     *prometheus.CounterVec
	AuthSuccesses    *prometheus.CounterVec
	AuthFailures     *prometheus.CounterVec
	AuthDuration     *prometheus.HistogramVec
	TokenValidations *prometheus.CounterVec

	// WebSocket connection metrics
	ActiveConnections *prometheus.GaugeVec
	TotalConnections  *prometheus.CounterVec
	MessagesSent      *prometheus.CounterVec
	MessagesReceived  *prometheus.CounterVec

	// Room and signaling metrics
	ActiveRooms       *prometheus.GaugeVec
	RoomJoins         *prometheus.CounterVec
	RoomLeaves        *prometheus.CounterVec
	SignalingMessages *prometheus.CounterVec

	// Server metrics
	ServerUptime      prometheus.Gauge
	ConfiguredThreads prometheus.Gauge
	ConfiguredRealms  *prometheus.GaugeVec

	// Memory metrics
	MemoryUsage    prometheus.Gauge
	HeapInUse      prometheus.Gauge
	HeapIdle       prometheus.Gauge
	HeapSys        prometheus.Gauge
	StackInUse     prometheus.Gauge
	GoroutineCount prometheus.Gauge
	GCCount        prometheus.Counter

	// Redis metrics
	RedisOperations *prometheus.CounterVec
	RedisErrors     *prometheus.CounterVec
	RedisLatency    *prometheus.HistogramVec
}

var (
	// Global metrics instance
	ServerMetrics *Metrics
	startTime     time.Time
)

// InitMetrics initializes all Prometheus metrics and registers them with the default registry
func InitMetrics(config *Config) {
	startTime = time.Now()

	ServerMetrics = &Metrics{
		// Authentication attempt counter by realm and result
		AuthAttempts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_auth_attempts_total",
				Help: "Total number of authentication attempts",
			},
			[]string{"realm", "result"},
		),

		// Successful authentication counter by realm
		AuthSuccesses: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_auth_success_total",
				Help: "Total number of successful authentications",
			},
			[]string{"realm", "user_id"},
		),

		// Failed authentication counter by realm and reason
		AuthFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_auth_failures_total",
				Help: "Total number of failed authentications",
			},
			[]string{"realm", "reason"},
		),

		// Authentication duration histogram
		AuthDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "turnip_auth_duration_seconds",
				Help:    "Duration of authentication requests",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"realm", "result"},
		),

		// Token validation counter by result
		TokenValidations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_token_validations_total",
				Help: "Total number of token validation attempts",
			},
			[]string{"result", "reason"},
		),

		// Active WebSocket connections gauge by realm
		ActiveConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "turnip_active_connections",
				Help: "Number of currently active WebSocket connections",
			},
			[]string{"realm"},
		),

		// Total WebSocket connections counter by realm
		TotalConnections: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_connections_total",
				Help: "Total number of WebSocket connections established",
			},
			[]string{"realm"},
		),

		// Messages sent counter by realm and message type
		MessagesSent: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_messages_sent_total",
				Help: "Total number of messages sent",
			},
			[]string{"realm", "message_type"},
		),

		// Messages received counter by realm and message type
		MessagesReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_messages_received_total",
				Help: "Total number of messages received",
			},
			[]string{"realm", "message_type"},
		),

		// Active rooms gauge by realm
		ActiveRooms: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "turnip_active_rooms",
				Help: "Number of currently active rooms",
			},
			[]string{"realm"},
		),

		// Room joins counter by realm
		RoomJoins: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_room_joins_total",
				Help: "Total number of room joins",
			},
			[]string{"realm"},
		),

		// Room leaves counter by realm
		RoomLeaves: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_room_leaves_total",
				Help: "Total number of room leaves",
			},
			[]string{"realm"},
		),

		// Signaling messages counter by realm and signal type
		SignalingMessages: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_signaling_messages_total",
				Help: "Total number of WebRTC signaling messages",
			},
			[]string{"realm", "signal_type"},
		),

		// Server uptime gauge
		ServerUptime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_server_uptime_seconds",
				Help: "Server uptime in seconds",
			},
		),

		// Configured threads gauge
		ConfiguredThreads: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_configured_threads",
				Help: "Number of configured server threads",
			},
		),

		// Configured realms gauge
		ConfiguredRealms: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "turnip_configured_realms",
				Help: "Configured realms for the server",
			},
			[]string{"realm"},
		),

		// Memory usage metrics
		MemoryUsage: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_memory_usage_bytes",
				Help: "Current memory usage in bytes",
			},
		),

		HeapInUse: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_heap_inuse_bytes",
				Help: "Bytes in in-use spans",
			},
		),

		HeapIdle: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_heap_idle_bytes",
				Help: "Bytes in idle (unused) spans",
			},
		),

		HeapSys: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_heap_sys_bytes",
				Help: "Bytes obtained from system for heap",
			},
		),

		StackInUse: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_stack_inuse_bytes",
				Help: "Bytes in stack spans",
			},
		),

		GoroutineCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "turnip_goroutines_count",
				Help: "Number of goroutines that currently exist",
			},
		),

		GCCount: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "turnip_gc_count_total",
				Help: "Total number of garbage collection cycles",
			},
		),

		// Redis operation metrics
		RedisOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_redis_operations_total",
				Help: "Total number of Redis operations",
			},
			[]string{"operation", "result"},
		),

		RedisErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "turnip_redis_errors_total",
				Help: "Total number of Redis errors",
			},
			[]string{"operation", "error_type"},
		),

		RedisLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "turnip_redis_latency_seconds",
				Help:    "Redis operation latency",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
			},
			[]string{"operation"},
		),
	}

	// Register all metrics with Prometheus
	prometheus.MustRegister(
		ServerMetrics.AuthAttempts,
		ServerMetrics.AuthSuccesses,
		ServerMetrics.AuthFailures,
		ServerMetrics.AuthDuration,
		ServerMetrics.TokenValidations,
		ServerMetrics.ActiveConnections,
		ServerMetrics.TotalConnections,
		ServerMetrics.MessagesSent,
		ServerMetrics.MessagesReceived,
		ServerMetrics.ActiveRooms,
		ServerMetrics.RoomJoins,
		ServerMetrics.RoomLeaves,
		ServerMetrics.SignalingMessages,
		ServerMetrics.ServerUptime,
		ServerMetrics.ConfiguredThreads,
		ServerMetrics.ConfiguredRealms,
		ServerMetrics.MemoryUsage,
		ServerMetrics.HeapInUse,
		ServerMetrics.HeapIdle,
		ServerMetrics.HeapSys,
		ServerMetrics.StackInUse,
		ServerMetrics.GoroutineCount,
		ServerMetrics.GCCount,
		ServerMetrics.RedisOperations,
		ServerMetrics.RedisErrors,
		ServerMetrics.RedisLatency,
	)

	// Set initial static metrics
	ServerMetrics.ConfiguredThreads.Set(float64(config.ThreadNum))
	ServerMetrics.ConfiguredRealms.WithLabelValues(config.Realm).Set(1)

	// Start background goroutine to update runtime metrics
	go updateRuntimeMetrics()

	log.Info().Msg("Prometheus metrics initialized and registered")
}

// SecurityMiddleware provides authentication for metrics endpoints
func SecurityMiddleware(config *Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Authentication check
			switch config.MetricsAuth {
			case "basic":
				if !basicAuth(w, r, config.MetricsUsername, config.MetricsPassword) {
					return
				}
			case "none":
				// No authentication required
			default:
				log.Warn().Str("auth_type", config.MetricsAuth).Msg("Unknown metrics auth type, defaulting to none")
			}

			// Log successful access
			log.Debug().
				Str("remote_addr", r.RemoteAddr).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Str("user_agent", r.UserAgent()).
				Msg("Metrics endpoint accessed")

			next.ServeHTTP(w, r)
		})
	}
}

// basicAuth implements HTTP Basic Authentication
func basicAuth(w http.ResponseWriter, r *http.Request, expectedUsername, expectedPassword string) bool {
	if expectedUsername == "" || expectedPassword == "" {
		log.Error().Msg("Basic auth configured but username/password not set")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return false
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Turnip Metrics"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return false
	}

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(username), []byte(expectedUsername)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(expectedPassword)) != 1 {
		log.Warn().
			Str("username", username).
			Str("remote_addr", r.RemoteAddr).
			Msg("Metrics basic auth failed")
		w.Header().Set("WWW-Authenticate", `Basic realm="Turnip Metrics"`)
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return false
	}

	return true
}

// StartMetricsServer starts the HTTP server for Prometheus metrics endpoint
func StartMetricsServer(config *Config) {
	if !config.EnableMetrics {
		log.Info().Msg("Metrics disabled in configuration")
		return
	}

	// Create HTTP server for metrics with security middleware
	mux := http.NewServeMux()
	securityMiddleware := SecurityMiddleware(config)

	// Protected metrics endpoint
	mux.Handle("/metrics", securityMiddleware(promhttp.Handler()))

	// Health check endpoint (no authentication required)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Protected info endpoint
	mux.HandleFunc("/info", securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		info := `{
			"service": "turnip-signaling",
			"version": "` + config.Version + `",
			"realm": "` + config.Realm + `",
			"threads": ` + strconv.Itoa(config.ThreadNum) + `,
			"metrics_enabled": ` + strconv.FormatBool(config.EnableMetrics) + `,
			"metrics_auth": "` + config.MetricsAuth + `",
			"metrics_bind_ip": "` + config.MetricsBindIP + `",
			"instance_id": "` + config.InstanceID + `"
		}`
		_, _ = w.Write([]byte(info))
	})).ServeHTTP)

	// Determine bind address
	bindAddr := config.MetricsBindIP + ":" + strconv.Itoa(config.MetricsPort)

	server := &http.Server{
		Addr:    bindAddr,
		Handler: mux,
	}

	// Start HTTP metrics server in a goroutine
	go func() {
		log.Info().
			Str("bind_addr", bindAddr).
			Str("auth", config.MetricsAuth).
			Str("endpoint", "/metrics").
			Msg("Starting Prometheus metrics server")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Failed to start metrics server")
		}
	}()

	// Log security configuration
	if config.MetricsAuth != "none" {
		log.Info().Str("auth_type", config.MetricsAuth).Msg("Metrics endpoint authentication enabled")
	}
	if config.MetricsBindIP != "0.0.0.0" {
		log.Info().Str("bind_ip", config.MetricsBindIP).Msg("Metrics endpoint bound to specific IP")
	}
}

// updateRuntimeMetrics continuously updates runtime and memory metrics
func updateRuntimeMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		UpdateMemoryMetrics()
		// Update server uptime
		if ServerMetrics != nil {
			ServerMetrics.ServerUptime.Set(time.Since(startTime).Seconds())
		}
	}
}

// RecordAuthAttempt records an authentication attempt
func RecordAuthAttempt(realm, result string) {
	if ServerMetrics != nil {
		ServerMetrics.AuthAttempts.WithLabelValues(realm, result).Inc()
	}
}

// RecordAuthSuccess records a successful authentication
func RecordAuthSuccess(realm, userID string) {
	if ServerMetrics != nil {
		ServerMetrics.AuthSuccesses.WithLabelValues(realm, userID).Inc()
	}
}

// RecordAuthFailure records a failed authentication
func RecordAuthFailure(realm, reason string) {
	if ServerMetrics != nil {
		ServerMetrics.AuthFailures.WithLabelValues(realm, reason).Inc()
	}
}

// RecordAuthDuration records authentication duration
func RecordAuthDuration(realm, result string, duration time.Duration) {
	if ServerMetrics != nil {
		ServerMetrics.AuthDuration.WithLabelValues(realm, result).Observe(duration.Seconds())
	}
}

// RecordTokenValidation records a token validation attempt
func RecordTokenValidation(result, reason string) {
	if ServerMetrics != nil {
		ServerMetrics.TokenValidations.WithLabelValues(result, reason).Inc()
	}
}

// RecordConnection records a new WebSocket connection
func RecordConnection(realm string) {
	if ServerMetrics != nil {
		ServerMetrics.TotalConnections.WithLabelValues(realm).Inc()
		ServerMetrics.ActiveConnections.WithLabelValues(realm).Inc()
	}
}

// RecordDisconnection records a WebSocket connection ending
func RecordDisconnection(realm string) {
	if ServerMetrics != nil {
		ServerMetrics.ActiveConnections.WithLabelValues(realm).Dec()
	}
}

// RecordMessageSent records a sent message
func RecordMessageSent(realm, messageType string) {
	if ServerMetrics != nil {
		ServerMetrics.MessagesSent.WithLabelValues(realm, messageType).Inc()
	}
}

// RecordMessageReceived records a received message
func RecordMessageReceived(realm, messageType string) {
	if ServerMetrics != nil {
		ServerMetrics.MessagesReceived.WithLabelValues(realm, messageType).Inc()
	}
}

// RecordRoomJoin records a room join
func RecordRoomJoin(realm string) {
	if ServerMetrics != nil {
		ServerMetrics.RoomJoins.WithLabelValues(realm).Inc()
		ServerMetrics.ActiveRooms.WithLabelValues(realm).Inc()
	}
}

// RecordRoomLeave records a room leave
func RecordRoomLeave(realm string) {
	if ServerMetrics != nil {
		ServerMetrics.RoomLeaves.WithLabelValues(realm).Inc()
		ServerMetrics.ActiveRooms.WithLabelValues(realm).Dec()
	}
}

// RecordSignalingMessage records a WebRTC signaling message
func RecordSignalingMessage(realm, signalType string) {
	if ServerMetrics != nil {
		ServerMetrics.SignalingMessages.WithLabelValues(realm, signalType).Inc()
	}
}

// RecordRedisOperation records a Redis operation
func RecordRedisOperation(operation, result string, duration time.Duration) {
	if ServerMetrics != nil {
		ServerMetrics.RedisOperations.WithLabelValues(operation, result).Inc()
		ServerMetrics.RedisLatency.WithLabelValues(operation).Observe(duration.Seconds())
	}
}

// RecordRedisError records a Redis error
func RecordRedisError(operation, errorType string) {
	if ServerMetrics != nil {
		ServerMetrics.RedisErrors.WithLabelValues(operation, errorType).Inc()
	}
}

// UpdateMemoryMetrics updates memory-related metrics
func UpdateMemoryMetrics() {
	if ServerMetrics == nil {
		return
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Update memory metrics
	ServerMetrics.MemoryUsage.Set(float64(m.Alloc))
	ServerMetrics.HeapInUse.Set(float64(m.HeapInuse))
	ServerMetrics.HeapIdle.Set(float64(m.HeapIdle))
	ServerMetrics.HeapSys.Set(float64(m.HeapSys))
	ServerMetrics.StackInUse.Set(float64(m.StackInuse))
	ServerMetrics.GoroutineCount.Set(float64(runtime.NumGoroutine()))

	// Update GC count (this is a counter, so we need to track the delta)
	static := getGCCountTracker()
	currentGC := m.NumGC
	if currentGC > static.lastGCCount {
		ServerMetrics.GCCount.Add(float64(currentGC - static.lastGCCount))
		static.lastGCCount = currentGC
	}
}

// gcCountTracker helps track GC count changes for the counter metric
type gcCountTracker struct {
	lastGCCount uint32
}

var gcTracker *gcCountTracker

func getGCCountTracker() *gcCountTracker {
	if gcTracker == nil {
		gcTracker = &gcCountTracker{lastGCCount: 0}
	}
	return gcTracker
}
