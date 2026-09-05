package turnip

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type App struct {
	config        Config
	redis         *redis.Client
	store         *RedisStore
	hub           *Hub
	metrics       *Metrics
	httpServer    *http.Server
	metricsServer *http.Server
}

func NewApp(config Config) (*App, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	options, err := redis.ParseURL(config.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	redisClient := redis.NewClient(options)
	metrics := NewMetrics()
	store := NewRedisStore(redisClient, metrics)
	authenticator := NewAuthenticator(config.JWTSecret, config.JWTIssuer, config.JWTAudience)
	hub := NewHub(store, authenticator, metrics, config.InstanceID, config.AllowedOrigins)

	app := &App{config: config, redis: redisClient, store: store, hub: hub, metrics: metrics}
	app.httpServer = &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           app.mainHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	if config.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metrics.Handler(config.MetricsUsername, config.MetricsPassword))
		app.metricsServer = &http.Server{
			Addr: config.MetricsAddr, Handler: mux,
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024,
		}
	}
	return app, nil
}

func (a *App) mainHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive", "version": a.config.Version})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if !a.hub.Ready() || a.store.Ping(ctx) != nil {
			a.metrics.ReadinessFailures.Inc()
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /v1/ws", a.hub.ServeWS)
	return mux
}

func (a *App) Run(ctx context.Context) error {
	startupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.store.Ping(startupCtx); err != nil {
		_ = a.redis.Close()
		return fmt.Errorf("connect to Redis: %w", err)
	}
	if err := a.hub.Start(ctx); err != nil {
		_ = a.redis.Close()
		return fmt.Errorf("subscribe to Redis events: %w", err)
	}

	errorsChannel := make(chan error, 2)
	go serveHTTP(a.httpServer, errorsChannel)
	if a.metricsServer != nil {
		go serveHTTP(a.metricsServer, errorsChannel)
	}
	log.Info().Str("http_addr", a.config.HTTPAddr).Str("version", a.config.Version).Msg("Turnip is ready")

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errorsChannel:
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = a.httpServer.Shutdown(shutdownCtx)
	if a.metricsServer != nil {
		_ = a.metricsServer.Shutdown(shutdownCtx)
	}
	a.hub.Shutdown(shutdownCtx)
	if err := a.redis.Close(); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

func serveHTTP(server *http.Server, errorsChannel chan<- error) {
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errorsChannel <- err
	}
}
