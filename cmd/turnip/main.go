package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liberocks/turnip/internal/turnip"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"
)

func main() {
	config, err := turnip.LoadConfig()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}
	if version != "dev" {
		config.Version = version
	}
	configureLogging(config)
	log.Info().Str("commit", commit).Str("built_at", builtAt).Msg("starting Turnip")
	app, err := turnip.NewApp(config)
	if err != nil {
		log.Error().Err(err).Msg("failed to initialize Turnip")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Error().Err(err).Msg("Turnip stopped with an error")
		os.Exit(1)
	}
	log.Info().Msg("Turnip stopped")
}

func configureLogging(config turnip.Config) {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	level, _ := zerolog.ParseLevel(config.LogLevel)
	zerolog.SetGlobalLevel(level)
	if config.LogFormat == "console" {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Str("service", "turnip").Logger()
		return
	}
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Str("service", "turnip").Logger()
}
