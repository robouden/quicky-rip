package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL       string
	HTTPAddr          string
	MailgunSigningKey string
	AnthropicAPIKey   string
	ModelFast         string // classify, parse, quote gates
	ModelStrong       string // synthesis
	AdminPassword     string // basic auth for /admin; empty disables auth
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		HTTPAddr:          envOr("HTTP_ADDR", ":8080"),
		MailgunSigningKey: os.Getenv("MAILGUN_SIGNING_KEY"),
		AnthropicAPIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		ModelFast:         envOr("MODEL_FAST", "claude-haiku-4-5-20251001"),
		ModelStrong:       envOr("MODEL_STRONG", "claude-sonnet-5"),
		AdminPassword:     os.Getenv("ADMIN_PASSWORD"),
	}
	for k, v := range map[string]string{
		"DATABASE_URL":        c.DatabaseURL,
		"MAILGUN_SIGNING_KEY": c.MailgunSigningKey,
		"ANTHROPIC_API_KEY":   c.AnthropicAPIKey,
	} {
		if v == "" {
			return c, fmt.Errorf("config: %s is required", k)
		}
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
