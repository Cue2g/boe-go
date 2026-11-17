// Package config lee la configuración desde variables de entorno.
package config

import (
	"os"
	"strconv"
	"time"

	"sboe/internal/notify"
)

type Config struct {
	DBPath   string
	MinDelay time.Duration
	MaxDelay time.Duration

	// Servidor web
	Addr         string
	BaseURL      string // URL pública, para los enlaces de los emails
	CookieSecure bool
	Location     *time.Location

	SMTP notify.SMTPConfig
}

func Load() Config {
	loadDotEnv()
	loc, err := time.LoadLocation(env("SBOE_TZ", "Europe/Madrid"))
	if err != nil {
		loc = time.UTC
	}
	port, _ := strconv.Atoi(env("SBOE_SMTP_PORT", "587"))
	return Config{
		DBPath:       env("SBOE_DB_PATH", "./data/sboe.db"),
		MinDelay:     dur("SBOE_MIN_DELAY", time.Second),
		MaxDelay:     dur("SBOE_MAX_DELAY", 3*time.Second),
		Addr:         env("SBOE_ADDR", ":8080"),
		BaseURL:      env("SBOE_BASE_URL", "http://localhost:8080"),
		CookieSecure: env("SBOE_COOKIE_SECURE", "") == "1" || env("SBOE_COOKIE_SECURE", "") == "true",
		Location:     loc,
		SMTP: notify.SMTPConfig{
			Host: os.Getenv("SBOE_SMTP_HOST"),
			Port: port,
			User: os.Getenv("SBOE_SMTP_USER"),
			Pass: os.Getenv("SBOE_SMTP_PASS"),
			From: os.Getenv("SBOE_SMTP_FROM"),
		},
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func dur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
