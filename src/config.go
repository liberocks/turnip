package main

import (
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type Config struct {
	Env          string `mapstructure:"ENV"`
	PublicIP     string `mapstructure:"PUBLIC_IP"`
	Port         int    `mapstructure:"PORT"`
	AccessSecret string `mapstructure:"ACCESS_SECRET"`
	LogLevel     string `mapstructure:"LOG_LEVEL"`
	Version      string `mapstructure:"VERSION"`
	Branch       string `mapstructure:"BRANCH"`
	BuiltAt      string `mapstructure:"BUILT_AT"`
	ThreadNum    int    `mapstructure:"THREAD_NUM"`
	Realm        string `mapstructure:"REALM"`

	// Redis configuration for multi-instance support
	RedisURL      string `mapstructure:"REDIS_URL"`
	RedisPassword string `mapstructure:"REDIS_PASSWORD"`
	RedisDB       int    `mapstructure:"REDIS_DB"`
	InstanceID    string `mapstructure:"INSTANCE_ID"`
}

var (
	Conf Config
	once sync.Once
)

// GetConfig is responsible to load env and get data and return the struct
func GetConfig() *Config {
	// Set default values
	viper.SetDefault("PORT", 5004)
	viper.SetDefault("LOG_LEVEL", "info")
	viper.SetDefault("PUBLIC_IP", "0.0.0.0")
	viper.SetDefault("REDIS_URL", "redis://localhost:6379")
	viper.SetDefault("REDIS_DB", 0)
	viper.SetDefault("ENV", "development")
	viper.SetDefault("REALM", "development")

	// Generate instance ID if not provided
	if os.Getenv("INSTANCE_ID") == "" {
		hostname, _ := os.Hostname()
		viper.SetDefault("INSTANCE_ID", hostname)
	}

	// Set THREAD_NUM default based on CPU count if not specified in environment
	if os.Getenv("THREAD_NUM") == "" {
		cpuCount := runtime.NumCPU()
		viper.SetDefault("THREAD_NUM", 2*cpuCount)
		log.Info().Int("cpu_count", cpuCount).Msg("THREAD_NUM not specified, using CPU count as default")
	} else {
		viper.SetDefault("THREAD_NUM", 2)
	}

	// Load environment variables from .env file
	viper.AutomaticEnv()
	viper.SetConfigFile(".env")
	_ = viper.ReadInConfig()

	// Read all environment variables and set them in Viper
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := parts[1]

		viper.Set(key, val)
	}

	// Print out all keys Viper knows about
	for _, key := range viper.AllKeys() {
		val := strings.Trim(viper.GetString(key), "\"")
		newKey := strings.ReplaceAll(key, "_", ".")
		viper.Set(newKey, val)
	}

	once.Do(func() {
		log.Info().Msg("Service configuration initialized.")
		err := viper.Unmarshal(&Conf)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed unmarshall config")
		}
	})

	return &Conf
}
