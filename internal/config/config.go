// Package config loads Reachly settings from environment variables.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr         string        // HTTP listen address
	HELO         string        // hostname announced in EHLO
	MailFrom     string        // envelope sender used in MAIL FROM
	Workers      int           // number of lookup workers
	SMTPTimeout  time.Duration // per-command SMTP timeout
	PerDomain    int           // max concurrent SMTP sessions per MX host
	ProbeDelay   time.Duration // pause between RCPT probes in one session
	GreylistWait time.Duration // wait before retrying a 4xx response
}

func Load() Config {
	return Config{
		Addr:         env("REACHLY_ADDR", ":8080"),
		HELO:         env("REACHLY_HELO", "reachly.local"),
		MailFrom:     env("REACHLY_MAIL_FROM", "verify@reachly.local"),
		Workers:      envInt("REACHLY_WORKERS", 10),
		SMTPTimeout:  envDur("REACHLY_SMTP_TIMEOUT", 10*time.Second),
		PerDomain:    envInt("REACHLY_PER_DOMAIN", 3),
		ProbeDelay:   envDur("REACHLY_PROBE_DELAY", 300*time.Millisecond),
		GreylistWait: envDur("REACHLY_GREYLIST_WAIT", 8*time.Second),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}
