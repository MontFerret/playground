// Package config parses and validates operator settings before startup.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Config contains validated service settings. Environment overrides defaults;
// explicitly empty values are invalid rather than interpreted as unset.
type Config struct {
	ListenAddr          string
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	MaxHeaderBytes      int
	ShutdownTimeout     time.Duration
	CleanupTimeout      time.Duration
	MaxRequestBodyBytes int
	MaxSourceBytes      int
	MaxBytecodeBytes    int
	MaxResponseBytes    int
}

// Load reads only the documented variables through lookup, without acquiring
// listeners or runtime resources. Errors identify settings, not their contents.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		ListenAddr:          "127.0.0.1:8080",
		ReadHeaderTimeout:   5 * time.Second,
		ReadTimeout:         10 * time.Second,
		WriteTimeout:        15 * time.Second,
		IdleTimeout:         60 * time.Second,
		MaxHeaderBytes:      16384,
		ShutdownTimeout:     5 * time.Second,
		CleanupTimeout:      5 * time.Second,
		MaxRequestBodyBytes: 1 << 20,
		MaxSourceBytes:      64 << 10,
		MaxBytecodeBytes:    1 << 20,
		MaxResponseBytes:    2 << 20,
	}

	if value, ok := lookup("PLAYGROUND_LISTEN_ADDR"); ok {
		cfg.ListenAddr = value
	}

	if strings.TrimSpace(cfg.ListenAddr) != cfg.ListenAddr {
		return Config{}, fmt.Errorf("PLAYGROUND_LISTEN_ADDR: expected host:port without surrounding whitespace")
	}

	host, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return Config{}, fmt.Errorf("PLAYGROUND_LISTEN_ADDR: expected host:port")
	}

	if !validListenHost(host) {
		return Config{}, fmt.Errorf("PLAYGROUND_LISTEN_ADDR: expected an IP address, hostname, or empty host")
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || strings.Trim(port, "0123456789") != "" || portNumber < 0 || portNumber > 65535 {
		return Config{}, fmt.Errorf("PLAYGROUND_LISTEN_ADDR: port must be an integer from 0 to 65535")
	}

	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{"PLAYGROUND_READ_HEADER_TIMEOUT", &cfg.ReadHeaderTimeout},
		{"PLAYGROUND_READ_TIMEOUT", &cfg.ReadTimeout},
		{"PLAYGROUND_WRITE_TIMEOUT", &cfg.WriteTimeout},
		{"PLAYGROUND_IDLE_TIMEOUT", &cfg.IdleTimeout},
		{"PLAYGROUND_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
		{"PLAYGROUND_CLEANUP_TIMEOUT", &cfg.CleanupTimeout},
	} {
		value, ok := lookup(setting.name)
		if !ok {
			continue
		}

		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("%s: expected a positive Go duration", setting.name)
		}

		*setting.target = duration
	}

	if value, ok := lookup("PLAYGROUND_MAX_HEADER_BYTES"); ok {
		size, err := strconv.Atoi(value)
		if err != nil || size <= 0 || size > int(^uint(0)>>1)-4096 {
			return Config{}, fmt.Errorf("PLAYGROUND_MAX_HEADER_BYTES: expected a positive integer within the HTTP server range")
		}

		cfg.MaxHeaderBytes = size
	}

	for _, setting := range []struct {
		name    string
		target  *int
		minimum int
	}{
		{"PLAYGROUND_MAX_REQUEST_BODY_BYTES", &cfg.MaxRequestBodyBytes, 1},
		{"PLAYGROUND_MAX_SOURCE_BYTES", &cfg.MaxSourceBytes, 1},
		{"PLAYGROUND_MAX_BYTECODE_BYTES", &cfg.MaxBytecodeBytes, 1},
		// Fixed sanitized error envelopes must always fit, including a newline.
		{"PLAYGROUND_MAX_RESPONSE_BYTES", &cfg.MaxResponseBytes, 256},
	} {
		value, ok := lookup(setting.name)
		if !ok {
			continue
		}

		size, err := strconv.Atoi(value)
		if err != nil || size < setting.minimum || size == int(^uint(0)>>1) {
			return Config{}, fmt.Errorf("%s: expected an integer of at least %d below the platform maximum", setting.name, setting.minimum)
		}

		*setting.target = size
	}

	return cfg, nil
}

func validListenHost(host string) bool {
	if host == "" {
		return true
	}

	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}

	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}

	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}

	return true
}
