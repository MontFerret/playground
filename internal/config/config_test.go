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
		MaxConcurrentExecutions: 2, AllowedOrigins: []string{"https://ferretlang.org"},
		ListenAddr: "127.0.0.1:8080", ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 16384,
		ShutdownTimeout: 5 * time.Second, CleanupTimeout: 5 * time.Second,
		MaxExecutionTimeout: 5 * time.Second, MaxOutputBytes: 1 << 20,
		MaxRequestBodyBytes: 1 << 20, MaxSourceBytes: 64 << 10, MaxBytecodeBytes: 1 << 20, MaxFormattedSourceBytes: 1 << 20, MaxResponseBytes: 2 << 20,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("defaults = %+v, want %+v", cfg, want)
	}
}

func TestCloudRunPort(t *testing.T) {
	for _, tc := range []struct {
		port, listen, want string
		valid              bool
	}{
		{"8087", "", "0.0.0.0:8087", true},
		{"1", "", "0.0.0.0:1", true},
		{"65535", "", "0.0.0.0:65535", true},
		{"", "", "", false},
		{"0", "", "", false},
		{"65536", "", "", false},
		{"+8080", "", "", false},
		{" 8080", "", "", false},
		{"secret-invalid-value", "", "", false},
		{"secret-invalid-value", "127.0.0.1:8081", "127.0.0.1:8081", true},
	} {
		cfg, err := Load(func(name string) (string, bool) {
			if name == "PORT" {
				return tc.port, true
			}

			return tc.listen, name == "PLAYGROUND_LISTEN_ADDR" && tc.listen != ""
		})
		if (err == nil) != tc.valid {
			t.Fatalf("PORT validity: %v", err)
		}

		if err != nil && (!strings.Contains(err.Error(), "PORT") || strings.Contains(err.Error(), "secret-invalid-value")) {
			t.Fatalf("PORT error was not sanitized: %v", err)
		}

		if err == nil && cfg.ListenAddr != tc.want {
			t.Fatalf("listen = %q, want %q", cfg.ListenAddr, tc.want)
		}
	}
}

func TestOverridesAndInvalidValues(t *testing.T) {
	valid := map[string]string{
		"PLAYGROUND_MAX_CONCURRENT_EXECUTIONS": "4",
		"PLAYGROUND_MAX_EXECUTION_TIMEOUT":     "123us", "PLAYGROUND_MAX_OUTPUT_BYTES": "37",
		"PLAYGROUND_LISTEN_ADDR": "[::1]:0", "PLAYGROUND_MAX_HEADER_BYTES": "8192",
		"PLAYGROUND_READ_HEADER_TIMEOUT": "1ns", "PLAYGROUND_READ_TIMEOUT": "2s",
		"PLAYGROUND_WRITE_TIMEOUT": "3s", "PLAYGROUND_IDLE_TIMEOUT": "4s",
		"PLAYGROUND_SHUTDOWN_TIMEOUT": "6s", "PLAYGROUND_CLEANUP_TIMEOUT": "7s",
		"PLAYGROUND_MAX_REQUEST_BODY_BYTES": "1024", "PLAYGROUND_MAX_SOURCE_BYTES": "512",
		"PLAYGROUND_MAX_FORMATTED_SOURCE_BYTES": "3072", "PLAYGROUND_MAX_BYTECODE_BYTES": "2048", "PLAYGROUND_MAX_RESPONSE_BYTES": "4096",
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

	if cfg.MaxExecutionTimeout != 123*time.Microsecond || cfg.MaxOutputBytes != 37 || cfg.MaxRequestBodyBytes != 1024 || cfg.MaxSourceBytes != 512 || cfg.MaxBytecodeBytes != 2048 || cfg.MaxFormattedSourceBytes != 3072 || cfg.MaxResponseBytes != 4096 {
		t.Fatalf("tooling limits not applied: %+v", cfg)
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

func TestToolingLimitBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, value string
		valid       bool
	}{
		{"PLAYGROUND_MAX_OUTPUT_BYTES", "1", true},
		{"PLAYGROUND_MAX_RESPONSE_BYTES", "255", false},
		{"PLAYGROUND_MAX_RESPONSE_BYTES", "256", true},
		{"PLAYGROUND_MAX_REQUEST_BODY_BYTES", "1", true},
		{"PLAYGROUND_MAX_SOURCE_BYTES", "1", true},
		{"PLAYGROUND_MAX_BYTECODE_BYTES", "1", true},
		{"PLAYGROUND_MAX_FORMATTED_SOURCE_BYTES", "1", true},
	} {
		_, err := Load(func(name string) (string, bool) { return test.value, name == test.name })
		if (err == nil) != test.valid {
			t.Errorf("%s boundary valid=%v: %v", test.name, test.valid, err)
		}
	}
}

func TestHostedOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		ok          bool
	}{
		{"PLAYGROUND_ALLOWED_ORIGINS", "https://ferretlang.org,http://localhost:8080", true},
		{"PLAYGROUND_ALLOWED_ORIGINS", "https://example.org/", false},
		{"PLAYGROUND_ALLOWED_ORIGINS", "*", false},
		{"PLAYGROUND_ALLOWED_ORIGINS", "https://a.example, https://b.example", false},
		{"PLAYGROUND_ALLOWED_ORIGINS", "https://user:secret@example.org", false},
		{"PLAYGROUND_ALLOWED_ORIGINS", "https://example.org:65536", false},
		{"PLAYGROUND_ALLOWED_ORIGINS", "null", false},
		{"PLAYGROUND_CDP_ORIGIN", "http://relay:9222", true},
		{"PLAYGROUND_CDP_ORIGIN", "http://relay:9222/private", false},
	} {
		_, err := Load(func(name string) (string, bool) { return tc.value, name == tc.name })
		if (err == nil) != tc.ok {
			t.Errorf("unexpected validation result for %s", tc.name)
		}
	}
}
