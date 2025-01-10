package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"time"

	"github.com/256dpi/newdns"
	"github.com/charmbracelet/log"
	"github.com/miekg/dns"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
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
	for zone, zcfg := range cfg.Zones {
		zone = newdns.NormalizeDomain(zone, true, true, false)

		slog := slog.With(
			"zone", zone)

		targets := make(map[string]string, len(zcfg))
		for name, target := range zcfg {
			target = newdns.NormalizeDomain(target, true, true, false)
			targets[name] = target

			slog.Debug(
				"added target into zone",
				"name", name,
				"target", target)
		}

		zones = append(zones, newdns.Zone{
			Name:             zone,
			MasterNameServer: hostname + ".",
			AllNameServers:   []string{hostname + ".", hostname + "."},
			Handler: func(name string) ([]newdns.Set, error) {
				slog := slog.With(
					"name", name)

				target, ok := targets[name]
				if !ok {
					slog.Debug(
						"no target found for name")
					return nil, nil
				}

				if cfg.Finalize {
					targetIPs, err := net.DefaultResolver.LookupIP(ctx, "ip", target)
					if err != nil {
						return nil, fmt.Errorf("failed to resolve target: %w", err)
					}

					slog.Debug(
						"resolved target to IPs",
						"target", target,
						"ips", targetIPs)

					return []newdns.Set{
						{
							Name:    joinDomain(name, zone),
							Type:    newdns.A,
							Records: ipsToDNSRecords(targetIPs),
							TTL:     time.Duration(cfg.Expire),
						},
					}, nil
				} else {
					return []newdns.Set{
						{
							Name:    joinDomain(name, zone),
							Type:    newdns.CNAME,
							Records: []newdns.Record{{Address: target}},
							TTL:     time.Duration(cfg.Expire),
						},
					}, nil
				}
			},
		})
	}

	if len(zones) == 0 {
		slog.Error(
			"no zones configured")
		os.Exit(1)
	}

	// create dnsHandler
	dnsHandler := newdns.NewServer(newdns.Config{
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

	errg, ctx := errgroup.WithContext(ctx)

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

		tss := tsnet.Server{
			Dir:       os.Getenv("CONFIGURATION_DIRECTORY"),
			Ephemeral: cfg.Tailscale.Ephemeral,
			Hostname:  cfg.Tailscale.Hostname,
			UserLogf: func(format string, args ...interface{}) {
				slog.Info(
					"Tailscale: "+fmt.Sprintf(format, args...),
					"component", "tailscale")
			},
		}
		defer tss.Close()

		tsStatus, err := tss.Up(ctx)
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
			"using Tailscale's first IPv4 address",
			"addr", firstV4)

		// Start UDP server:
		errg.Go(func() error {
			conn, err := tss.ListenPacket("udp", netip.AddrPortFrom(firstV4, 53).String())
			if err != nil {
				return fmt.Errorf("failed to listen to UDP on Tailscale: %w", err)
			}
			defer closeHandleErr(conn)

			slog := slog.With(
				"conn.local_addr", conn.LocalAddr())
			slog.Info("UDP DNS server starting via Tailscale")

			dnss := newDNSServer("udp", dnsMux)
			dnss.PacketConn = conn

			errg.Go(func() error {
				ctxWaitShutdown(ctx, dnss)
				return nil
			})

			return dnss.ActivateAndServe()
		})

		// Start TCP server:
		errg.Go(func() error {
			conn, err := tss.Listen("tcp", netip.AddrPortFrom(firstV4, 53).String())
			if err != nil {
				return fmt.Errorf("failed to listen to TCP on Tailscale: %w", err)
			}
			defer closeHandleErr(conn)

			slog := slog.With(
				"conn.local_addr", conn.Addr())
			slog.Info("TCP DNS server starting via Tailscale")

			dnss := newDNSServer("tcp", dnsMux)
			dnss.Listener = conn

			errg.Go(func() error {
				ctxWaitShutdown(ctx, dnss)
				return nil
			})

			return dnss.ActivateAndServe()
		})
	} else {
		slog.Info(
			"DNS server starting",
			"addr", cfg.Addr)

		// Start UDP server:
		errg.Go(func() error {
			dnss := newDNSServer("udp", dnsMux)
			dnss.Addr = cfg.Addr

			errg.Go(func() error {
				ctxWaitShutdown(ctx, dnss)
				return nil
			})

			return dnss.ListenAndServe()
		})

		// Start TCP server:
		errg.Go(func() error {
			dnss := newDNSServer("tcp", dnsMux)
			dnss.Addr = cfg.Addr

			errg.Go(func() error {
				ctxWaitShutdown(ctx, dnss)
				return nil
			})

			return dnss.ListenAndServe()
		})
	}

	if err := errg.Wait(); err != nil {
		slog.Error(
			"failed to run server",
			"err", err)
		return 1
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

func newDNSServer(network string, mux *dns.ServeMux) *dns.Server {
	return &dns.Server{
		Net:           network,
		Handler:       mux,
		MsgAcceptFunc: newdns.Accept(logDNSEvent),
	}
}

func ipsToDNSRecords(ips []net.IP) []newdns.Record {
	records := make([]newdns.Record, 0, len(ips))
	for _, ip := range ips {
		records = append(records, newdns.Record{
			Address: ip.String(),
		})
	}
	return records
}

func joinDomain(name, zone string) string {
	if zone == "." {
		return name
	}
	if name == "" {
		return zone
	}
	return name + "." + zone
}

func ctxWaitShutdown(ctx context.Context, shutdowner interface {
	Shutdown() error
}) {
	<-ctx.Done()

	slog.Info("shutting down server")

	if err := shutdowner.Shutdown(); err != nil {
		slog.Warn(
			"failed to shutdown server",
			"err", err)
	}
}

func closeHandleErr(closer io.Closer) {
	if err := closer.Close(); err != nil {
		slog.Warn(
			"failed to close",
			"err", err)
	}
}
