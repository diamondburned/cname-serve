package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/256dpi/newdns"
	"github.com/charmbracelet/log"
	"github.com/miekg/dns"
	"github.com/spf13/pflag"
)

var (
	configPath = "config.toml"
	verbose    = false
)

func init() {
	pflag.StringVarP(&configPath, "config", "c", configPath, "path to config file")
	pflag.BoolVarP(&verbose, "verbose", "v", verbose, "print debug logs")
}

func main() {
	pflag.Parse()

	handler := log.NewWithOptions(os.Stderr, log.Options{
		Level: log.DebugLevel,
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	os.Exit(run(ctx))
}

func run(ctx context.Context) int {
	cfg, err := ParseConfigFile(configPath)
	if err != nil {
		slog.Error(
			"failed to parse config file",
			"path", configPath,
			"err", err)
		return 1
	}

	hostname, err := os.Hostname()
	if err != nil {
		slog.Error(
			"failed to get hostname",
			"err", err)
		return 1
	}

	zones := make([]newdns.Zone, 0, len(cfg.Zones))
	zoneNames := make([]string, 0, len(cfg.Zones))

	for zone, zcfg := range cfg.Zones {
		zone = newdns.NormalizeDomain(zone, true, true, false)
		zoneSets := make(map[string][]newdns.Set, len(zcfg))

		for name, addr := range zcfg {
			addr = newdns.NormalizeDomain(addr, true, true, false)

			fullName := zone
			if name != "" {
				fullName = name + "." + zone
			}

			zoneSets[name] = []newdns.Set{
				{
					Name: fullName,
					Type: newdns.CNAME,
					Records: []newdns.Record{
						{Address: addr},
					},
				},
			}
		}

		zones = append(zones, newdns.Zone{
			Name:             zone,
			NSTTL:            time.Duration(cfg.Expire),
			MinTTL:           time.Duration(cfg.Expire),
			MasterNameServer: hostname + ".",
			AllNameServers:   []string{hostname + ".", hostname + "."},
			Handler: func(name string) ([]newdns.Set, error) {
				if sets, ok := zoneSets[name]; ok {
					return sets, nil
				}
				return nil, nil
			},
		})

		zoneNames = append(zoneNames, zone)

		slog.Debug(
			"added zone",
			"zone", zone,
			"zone.sets", len(zoneSets))
	}

	if len(zones) == 0 {
		slog.Error(
			"no zones configured")
		os.Exit(1)
	}

	// create server
	server := newdns.NewServer(newdns.Config{
		Zones:    zoneNames,
		Fallback: cfg.FallbackDNS,
		Handler: func(name string) (*newdns.Zone, error) {
			for _, zone := range zones {
				if newdns.InZone(zone.Name, name) {
					return &zone, nil
				}
			}
			return nil, nil
		},
		Logger: func(e newdns.Event, msg *dns.Msg, err error, reason string) {
			slog := slog.With(
				"event", e.String(),
				"message", msg,
			)
			if err != nil {
				slog.Error(
					"dns error",
					"err", err)
			} else {
				slog.Debug(
					"dns event")
			}
		},
	})

	go func() {
		<-ctx.Done()
		server.Close()
	}()

	slog.Info(
		"DNS server starting",
		"addr", cfg.Addr)

	if err := server.Run(cfg.Addr); err != nil {
		slog.Error(
			"failed to run DNS server",
			"err", err)
		os.Exit(1)
	}

	return 0
}
