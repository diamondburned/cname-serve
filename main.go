package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"time"

	"github.com/256dpi/newdns"
	"github.com/charmbracelet/log"
	"github.com/miekg/dns"
	"github.com/spf13/pflag"
	"tailscale.com/tsnet"
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

	// create dnsHandler
	dnsHandler := newdns.NewServer(newdns.Config{
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
		Logger: logDNSEvent,
	})

	dnsMux := dns.NewServeMux()

	// Add in all zones.
	for _, zone := range zones {
		dnsMux.Handle(zone.Name, dnsHandler)
	}

	// Add in fallback if available.
	if cfg.FallbackDNS != "" {
		dnsMux.Handle(".", newdns.Proxy(cfg.FallbackDNS, logDNSEvent))
	}

	dnsServer := &dns.Server{
		Net:           "udp",
		Addr:          cfg.Addr,
		Handler:       dnsMux,
		MsgAcceptFunc: newdns.Accept(logDNSEvent),
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down DNS server")

		if err := dnsServer.Shutdown(); err != nil {
			slog.Error(
				"failed to shutdown UDP server",
				"err", err)
		}
	}()

	if cfg.Tailscale.Enable {
		authKey := os.Getenv("TS_AUTHKEY")
		if authKey == "" {
			slog.Warn(
				"Tailscale auth key not set",
				"want_env", "TS_AUTHKEY")
			return 1
		}

		if cfg.Addr != ":53" {
			slog.Error(
				"server must be configured to listen to port 53 when using Tailscale",
				"want_addr", ":53")
			return 1
		}

		s := tsnet.Server{
			Ephemeral: true,
			Hostname:  cfg.Tailscale.Hostname,
			UserLogf: func(format string, args ...interface{}) {
				slog.Info(
					"Tailscale: "+fmt.Sprintf(format, args...),
					"component", "tailscale")
			},
		}
		defer s.Close()

		tsStatus, err := s.Up(ctx)
		if err != nil {
			slog.Error(
				"failed to bring up Tailscale connection",
				"err", err)
			return 1
		}

		slog := slog.With(
			"ts.node_id", tsStatus.Self.ID,
			"ts.hostname", tsStatus.Self.HostName)

		slog.Debug("Tailscale connection up")

		firstV4Ix := slices.IndexFunc(tsStatus.TailscaleIPs, netip.Addr.Is4)
		if firstV4Ix == -1 {
			slog.Error(
				"no IPv4 address found in given Tailscale IPs",
				"ips", tsStatus.TailscaleIPs)
			return 1
		}

		firstV4 := tsStatus.TailscaleIPs[firstV4Ix]
		slog.Debug(
			"using first IPv4 address",
			"addr", firstV4)

		conn, err := s.ListenPacket("udp", netip.AddrPortFrom(firstV4, 53).String())
		if err != nil {
			slog.Error(
				"failed to listen to UDP on Tailscale",
				"hostname", cfg.Tailscale.Hostname,
				"err", err)
			return 1
		}

		slog = slog.With(
			"conn.local_addr", conn.LocalAddr(),
		)

		slog.Info("DNS server starting via Tailscale")

		dnsServer.PacketConn = conn
		if err := dnsServer.ActivateAndServe(); err != nil {
			slog.Error(
				"failed to run UDP server on Tailscale",
				"err", err)
			return 1
		}
	} else {
		slog.Info(
			"DNS server starting",
			"addr", cfg.Addr)

		if err := dnsServer.ListenAndServe(); err != nil {
			slog.Error(
				"failed to run UDP server",
				"err", err)
			return 1
		}
	}

	return 0
}

func logDNSEvent(e newdns.Event, msg *dns.Msg, err error, reason string) {
	slog := slog.With(
		"event", e.String(),
		"message", msg,
	)
	if err != nil {
		slog.Error(
			"DNS error",
			"err", err)
	} else {
		slog.Debug(
			"DNS event")
	}
}
