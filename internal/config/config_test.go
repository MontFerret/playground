package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}

	want := Config{
		ListenAddr: "127.0.0.1:8080", ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 16384,
		ShutdownTimeout: 5 * time.Second, CleanupTimeout: 5 * time.Second,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("defaults = %+v, want %+v", cfg, want)
	}
}

func TestOverridesAndInvalidValues(t *testing.T) {
	valid := map[string]string{
		"PLAYGROUND_LISTEN_ADDR": "[::1]:0", "PLAYGROUND_MAX_HEADER_BYTES": "8192",
		"PLAYGROUND_READ_HEADER_TIMEOUT": "1ns", "PLAYGROUND_READ_TIMEOUT": "2s",
		"PLAYGROUND_WRITE_TIMEOUT": "3s", "PLAYGROUND_IDLE_TIMEOUT": "4s",
		"PLAYGROUND_SHUTDOWN_TIMEOUT": "6s", "PLAYGROUND_CLEANUP_TIMEOUT": "7s",
	}
	cfg, err := Load(func(name string) (string, bool) {
		value, ok := valid[name]

		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddr != "[::1]:0" || cfg.MaxHeaderBytes != 8192 || cfg.ReadHeaderTimeout != time.Nanosecond || cfg.ReadTimeout != 2*time.Second || cfg.WriteTimeout != 3*time.Second || cfg.IdleTimeout != 4*time.Second || cfg.ShutdownTimeout != 6*time.Second || cfg.CleanupTimeout != 7*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}

	for name := range valid {
		values := []string{"", "secret-invalid-value"}
		if name == "PLAYGROUND_LISTEN_ADDR" {
			values = append(values, "127.0.0.1:-1", "127.0.0.1:65536", "localhost:http", "localhost:+1", " :8080 ", "bad host:80", "user@host:80", "-host:80", "host..name:80")
		} else {
			values = append(values, "0", "-1", "99999999999999999999999999999999999999")
			if name != "PLAYGROUND_MAX_HEADER_BYTES" {
				values = append(values, "-1s", "0s")
			}
		}

		for _, value := range values {
			t.Run(name+"/"+value, func(t *testing.T) {
				_, err := Load(func(key string) (string, bool) { return value, key == name })
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("expected setting-specific error, got %v", err)
				}

				if strings.Contains(err.Error(), "secret-invalid-value") {
					t.Fatal("error exposed configuration contents")
				}
			})
		}
	}
}
