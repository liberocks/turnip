package turnip

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var instanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

const (
	DefaultHTTPAddr    = ":5004"
	DefaultMetricsAddr = "127.0.0.1:9090"
	MaxMessageBytes    = 64 * 1024
	MaxRoomConnections = 32
	MessagesPerSecond  = 50
	MessageBurst       = 100
)

type Config struct {
	HTTPAddr        string
	RedisURL        string
	InstanceID      string
	JWTSecret       string
	JWTIssuer       string
	JWTAudience     string
	AllowedOrigins  []string
	MetricsAddr     string
	MetricsUsername string
	MetricsPassword string
	LogLevel        string
	LogFormat       string
	Version         string
}

func LoadConfig() (Config, error) {
	hostname, _ := os.Hostname()
	cfg := Config{
		HTTPAddr:        envOr("TURNIP_HTTP_ADDR", DefaultHTTPAddr),
		RedisURL:        envOr("TURNIP_REDIS_URL", "redis://localhost:6379/0"),
		InstanceID:      envOr("TURNIP_INSTANCE_ID", hostname),
		JWTSecret:       os.Getenv("TURNIP_JWT_SECRET"),
		JWTIssuer:       os.Getenv("TURNIP_JWT_ISSUER"),
		JWTAudience:     os.Getenv("TURNIP_JWT_AUDIENCE"),
		AllowedOrigins:  splitCSV(os.Getenv("TURNIP_ALLOWED_ORIGINS")),
		MetricsAddr:     envOr("TURNIP_METRICS_ADDR", DefaultMetricsAddr),
		MetricsUsername: os.Getenv("TURNIP_METRICS_USERNAME"),
		MetricsPassword: os.Getenv("TURNIP_METRICS_PASSWORD"),
		LogLevel:        envOr("TURNIP_LOG_LEVEL", "info"),
		LogFormat:       envOr("TURNIP_LOG_FORMAT", "console"),
		Version:         envOr("TURNIP_VERSION", "dev"),
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if err := validateAddress("TURNIP_HTTP_ADDR", c.HTTPAddr, false); err != nil {
		return err
	}
	if c.MetricsAddr != "" {
		if err := validateAddress("TURNIP_METRICS_ADDR", c.MetricsAddr, true); err != nil {
			return err
		}
		if c.MetricsAddr == c.HTTPAddr {
			return fmt.Errorf("TURNIP_METRICS_ADDR must differ from TURNIP_HTTP_ADDR")
		}
	}
	redisURL, err := url.Parse(c.RedisURL)
	if err != nil || (redisURL.Scheme != "redis" && redisURL.Scheme != "rediss") || redisURL.Host == "" {
		return fmt.Errorf("TURNIP_REDIS_URL must be a redis:// or rediss:// URL")
	}
	if !instanceIDPattern.MatchString(c.InstanceID) {
		return fmt.Errorf("TURNIP_INSTANCE_ID must contain 1-128 letters, digits, dots, underscores, or hyphens")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("TURNIP_JWT_SECRET must contain at least 32 bytes")
	}
	if strings.TrimSpace(c.JWTIssuer) == "" {
		return fmt.Errorf("TURNIP_JWT_ISSUER is required")
	}
	if strings.TrimSpace(c.JWTAudience) == "" {
		return fmt.Errorf("TURNIP_JWT_AUDIENCE is required")
	}
	if (c.MetricsUsername == "") != (c.MetricsPassword == "") {
		return fmt.Errorf("TURNIP_METRICS_USERNAME and TURNIP_METRICS_PASSWORD must be set together")
	}
	if c.LogFormat != "console" && c.LogFormat != "json" {
		return fmt.Errorf("TURNIP_LOG_FORMAT must be console or json")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("TURNIP_LOG_LEVEL must be debug, info, warn, or error")
	}
	for _, origin := range c.AllowedOrigins {
		if origin == "*" {
			continue
		}
		u, parseErr := url.Parse(origin)
		if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid allowed origin %q", origin)
		}
	}
	return nil
}

func validateAddress(name, address string, allowEmpty bool) error {
	if address == "" && allowEmpty {
		return nil
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return fmt.Errorf("%s must be a host:port address", name)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
