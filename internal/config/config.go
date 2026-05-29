package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Server ServerConfig `json:"server"`
	Resin  ResinConfig  `json:"resin"`
	Legacy LegacyConfig `json:"legacy"`
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type ResinConfig struct {
	Mode string `json:"mode"`
}

type LegacyConfig struct {
	CookieFile string `json:"cookie_file"`
}

func Default() Config {
	return Config{
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
}

func LoadConfig(path string) (Config, error) {
	defaultCfg := Default()
	cfg := defaultCfg

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Server.Port == 0 {
		cfg.Server.Port = defaultCfg.Server.Port
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = defaultCfg.Server.Host
	}
	if cfg.Resin.Mode == "" {
		cfg.Resin.Mode = defaultCfg.Resin.Mode
	}

	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return Config{}, fmt.Errorf("server.port must be in range 1-65535")
	}

	switch cfg.Resin.Mode {
	case "reverse", "forward", "connect", "socks5":
	default:
		return Config{}, fmt.Errorf("invalid resin.mode: must be one of reverse|forward|connect|socks5")
	}

	if cfg.Legacy.CookieFile != "" {
		return Config{}, fmt.Errorf("legacy.cookie_file is deprecated and must be empty")
	}

	return cfg, nil
}
