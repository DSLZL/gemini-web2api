package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	got := Default()
	want := Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8081,
		},
		Resin: ResinConfig{
			Mode: "reverse",
		},
		Legacy: LegacyConfig{
			CookieFile: "",
		},
	}
	if got != want {
		t.Fatalf("unexpected default config, got %+v want %+v", got, want)
	}
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{},"resin":{}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	def := Default()
	if cfg != def {
		t.Fatalf("expected config to match defaults, got %+v want %+v", cfg, def)
	}
}

func TestLoadConfigReadFileError(t *testing.T) {
	t.Parallel()

	missingPath := filepath.Join(t.TempDir(), "missing-config.json")
	_, err := LoadConfig(missingPath)
	if err == nil {
		t.Fatal("expected read config error for missing file")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("expected error mentioning read config, got %v", err)
	}
}

func TestLoadConfigRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected parse config error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("expected error mentioning parse config, got %v", err)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{"host":"127.0.0.1","port":8081},"resin":{"mode":"forward"},"unknown":true}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected parse config error for unknown field")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("expected error mentioning parse config, got %v", err)
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field parse error, got %v", err)
	}
}

func TestLoadConfigExplicitZeroAndEmptyFallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{"port":0,"host":""},"resin":{"mode":""}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	def := Default()
	if cfg.Server.Port != def.Server.Port {
		t.Fatalf("expected fallback server.port %d, got %d", def.Server.Port, cfg.Server.Port)
	}
	if cfg.Server.Host != def.Server.Host {
		t.Fatalf("expected fallback server.host %q, got %q", def.Server.Host, cfg.Server.Host)
	}
	if cfg.Resin.Mode != def.Resin.Mode {
		t.Fatalf("expected fallback resin.mode %q, got %q", def.Resin.Mode, cfg.Resin.Mode)
	}
}

func TestLoadConfigPreservesExplicitValidValues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{"port":9090,"host":"127.0.0.1"},"resin":{"mode":"forward"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Fatalf("expected explicit server.port 9090 to be preserved, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("expected explicit server.host 127.0.0.1 to be preserved, got %q", cfg.Server.Host)
	}
	if cfg.Resin.Mode != "forward" {
		t.Fatalf("expected explicit resin.mode forward to be preserved, got %q", cfg.Resin.Mode)
	}
}

func TestLoadConfigAcceptsAllowedResinModes(t *testing.T) {
	t.Parallel()

	tests := []string{"reverse", "forward", "connect", "socks5"}
	for _, mode := range tests {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			content := `{"server":{"host":"127.0.0.1","port":8081},"resin":{"mode":"` + mode + `"}}`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}

			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if cfg.Resin.Mode != mode {
				t.Fatalf("expected resin.mode %q, got %q", mode, cfg.Resin.Mode)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		port int
	}{
		{name: "non_positive", port: -1},
		{name: "too_large", port: 70000},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			content := `{"server":{"port":` + strconv.Itoa(tt.port) + `,"host":"127.0.0.1"}}`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}

			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("expected error for invalid server.port")
			}
			if !strings.Contains(err.Error(), "server.port") {
				t.Fatalf("expected error mentioning server.port, got %v", err)
			}
			if !strings.Contains(err.Error(), "range 1-65535") {
				t.Fatalf("expected error mentioning port constraint range 1-65535, got %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidResinMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"resin":{"mode":"invalid-mode"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid resin.mode")
	}
	if !strings.Contains(err.Error(), "resin.mode") {
		t.Fatalf("expected error mentioning resin.mode, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid resin.mode") {
		t.Fatalf("expected error mentioning invalid resin.mode constraint, got %v", err)
	}
}

func TestLoadConfigRejectsLegacyCookieFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"legacy":{"cookie_file":"cookies.txt"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error when legacy.cookie_file is non-empty")
	}
	if !strings.Contains(err.Error(), "legacy.cookie_file") {
		t.Fatalf("expected error mentioning legacy.cookie_file, got %v", err)
	}
}
