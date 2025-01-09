package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Addr        string                `toml:"addr"`
	Expire      tomlDuration          `toml:"expire"`
	FallbackDNS string                `toml:"fallback_dns"`
	Tailscale   TailscaleConfig       `toml:"tailscale"`
	Zones       map[string]ZoneConfig `toml:"zones"`
}

type ZoneConfig map[string]string

type TailscaleConfig struct {
	Enable    bool   `toml:"enable"`
	Ephemeral bool   `toml:"ephemeral"`
	Hostname  string `toml:"hostname"`
}

type tomlDuration time.Duration

func (d *tomlDuration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = tomlDuration(v)
	return nil
}

func defaultConfig() *Config {
	return &Config{
		Addr:        ":53",
		Expire:      tomlDuration(5 * time.Second),
		FallbackDNS: "100.100.100.100:53",
		Tailscale: TailscaleConfig{
			Enable:   false,
			Hostname: "cname-serve",
		},
	}
}

func ParseConfigFile(path string) (*Config, error) {
	slog.Debug(
		"parsing config file",
		"path", path)

	d, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := defaultConfig()
	if err := toml.Unmarshal(d, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}
