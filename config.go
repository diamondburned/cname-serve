package main

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Addr        string                `toml:"addr"`
	Expire      tomlDuration          `toml:"expire"`
	FallbackDNS string                `toml:"fallback_dns"`
	Zones       map[string]ZoneConfig `toml:"zone"`
}

type ZoneConfig map[string]string

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
		Expire: tomlDuration(30 * time.Second),
	}
}

func ParseConfigFile(path string) (*Config, error) {
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
