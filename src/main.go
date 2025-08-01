package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Initialize configuration
	config := GetConfig()

	// Configure logging
	configureLogging(config.LogLevel)

	// Validate required configuration
	if config.AccessSecret == "" {
		log.Fatal().Msg("ACCESS_SECRET environment variable is required")
	}

	log.Info().
		Str("service", "turnip-signaling").
		Str("version", config.Version).
		Str("branch", config.Branch).
		Str("built_at", config.BuiltAt).
		Int("port", config.Port).
		Int("threads", config.ThreadNum).
		Str("instance_id", config.InstanceID).
		Str("redis_url", config.RedisURL).
		Str("environment", os.Getenv("ENVIRONMENT")).
		Msg("Starting Turnip WebRTC Signaling Server")

	// Set GOMAXPROCS
	runtime.GOMAXPROCS(config.ThreadNum)

	// Initialize Redis client
	redisOpts, err := redis.ParseURL(config.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse Redis URL")
	}

	if config.RedisPassword != "" {
		redisOpts.Password = config.RedisPassword
	}
	redisOpts.DB = config.RedisDB

	redisClient := redis.NewClient(redisOpts)

	// Test Redis connection
	ctx := context.Background()
	_, err = redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Redis")
	}

	log.Info().Str("redis_url", config.RedisURL).Msg("Connected to Redis")

	// Create and start the Redis hub
	hub := NewHub(redisClient, config.InstanceID)
	go hub.Run()

	// Setup routes
	http.HandleFunc("/health", HealthHandler)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		WebSocketHandler(hub, w, r)
	})

	if config.Env == "development" {
		// Token generation endpoint for testing
		http.HandleFunc("/generate-token", TokenHandler)

		// Serve test client
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.ServeFile(w, r, "test-client.html")
			} else {
				http.NotFound(w, r)
			}
		})
	}

	// Start server
	addr := fmt.Sprintf("%s:%d", config.PublicIP, config.Port)
	log.Info().
		Str("address", addr).
		Str("event", "server_starting").
		Msg("Server starting")

	// Create HTTP server
	server := &http.Server{
		Addr: addr,
	}

	// Handle graceful shutdown
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c

		log.Info().Msg("Shutting down server...")

		// Shutdown Redis hub
		hub.Shutdown()

		// Shutdown HTTP server
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Server shutdown error")
		}

		// Close Redis connection
		if err := redisClient.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close Redis connection")
		}

		log.Info().Msg("Server shutdown complete")
		os.Exit(0)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().
			Err(err).
			Str("address", addr).
			Str("event", "server_failed").
			Msg("Server failed to start")
	}
}

func configureLogging(level string) {
	// Configure zerolog with proper JSON formatting
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.LevelFieldName = "level"
	zerolog.TimestampFieldName = "timestamp"
	zerolog.MessageFieldName = "message"

	// Set log level
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Always output JSON in production, pretty console in development
	environment := os.Getenv("ENVIRONMENT")
	if environment == "production" {
		// Pure JSON output for production
		log.Logger = zerolog.New(os.Stdout).With().
			Timestamp().
			Str("service", "turnip-signaling").
			Logger()
	} else {
		// Pretty console output for development with JSON structure available
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
			NoColor:    false,
		}
		log.Logger = zerolog.New(consoleWriter).With().
			Timestamp().
			Str("service", "turnip-signaling").
			Logger()
	}

	log.Info().
		Str("level", level).
		Str("environment", environment).
		Str("event", "logging_configured").
		Msg("Logging configuration initialized")
}
