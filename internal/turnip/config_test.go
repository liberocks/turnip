package turnip

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		HTTPAddr: ":5004", RedisURL: "redis://localhost:6379/0", InstanceID: "instance-1",
		JWTSecret: strings.Repeat("s", 32), JWTIssuer: "turnip-test", JWTAudience: "turnip",
		MetricsAddr: "127.0.0.1:9090", LogLevel: "info", LogFormat: "json",
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		valid  bool
	}{
		{name: "valid", mutate: func(*Config) {}, valid: true},
		{name: "short secret", mutate: func(c *Config) { c.JWTSecret = "short" }},
		{name: "missing issuer", mutate: func(c *Config) { c.JWTIssuer = "" }},
		{name: "missing audience", mutate: func(c *Config) { c.JWTAudience = "" }},
		{name: "invalid Redis scheme", mutate: func(c *Config) { c.RedisURL = "http://localhost" }},
		{name: "partial metrics auth", mutate: func(c *Config) { c.MetricsUsername = "admin" }},
		{name: "invalid origin path", mutate: func(c *Config) { c.AllowedOrigins = []string{"https://example.com/path"} }},
		{name: "allowed wildcard", mutate: func(c *Config) { c.AllowedOrigins = []string{"*"} }, valid: true},
		{name: "metrics disabled", mutate: func(c *Config) { c.MetricsAddr = "" }, valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig()
			test.mutate(&config)
			if err := config.Validate(); (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestLoadConfigUsesOnlyTurnipEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("TURNIP_JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("TURNIP_JWT_ISSUER", "issuer")
	t.Setenv("TURNIP_JWT_AUDIENCE", "audience")
	t.Setenv("TURNIP_METRICS_ADDR", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.HTTPAddr != DefaultHTTPAddr || config.LogFormat != "console" || config.MetricsAddr != "" {
		t.Fatalf("unexpected defaults: %+v", config)
	}
}
