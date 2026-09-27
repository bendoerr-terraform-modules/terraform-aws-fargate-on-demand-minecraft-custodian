// Command custodian is the on-demand Minecraft ECS sidecar (see the design spec).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/awsx"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/config"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/health"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/platform"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/watcher/mcjava"
)

//nolint:gochecknoglobals // Set at build time via -ldflags "-X main.version=...".
var version = "dev"

const (
	exitUsage          = 2
	healthCheckTimeout = time.Second
	metadataTimeout    = 5 * time.Second
	loopback           = "127.0.0.1"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout))
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) int {
	if len(args) == 0 {
		return serve(ctx, getenv, out)
	}
	switch args[0] {
	case "healthcheck":
		return healthcheck(ctx, getenv)
	case "version":
		_, _ = fmt.Fprintln(out, version)
		return lifecycle.ExitOK
	default:
		_, _ = fmt.Fprintf(out, "unknown command %q (want: healthcheck, version, or none)\n", args[0])
		return exitUsage
	}
}

func healthcheck(ctx context.Context, getenv func(string) string) int {
	port, err := config.HealthPort(getenv)
	if err != nil {
		return lifecycle.ExitError
	}
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	if err = health.Check(ctx, net.JoinHostPort(loopback, strconv.Itoa(port))); err != nil {
		return lifecycle.ExitError
	}
	return lifecycle.ExitOK
}

func serve(ctx context.Context, getenv func(string) string, out io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		newLogger(out, slog.LevelInfo).ErrorContext(ctx, "invalid configuration", slog.Any("error", err))
		return lifecycle.ExitError
	}
	logger := newLogger(out, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "cannot load AWS configuration; the service cannot be reaped", slog.Any("error", err))
		return lifecycle.ExitError
	}
	ecsClient := ecs.NewFromConfig(awsCfg)

	prober, proberErr := mcjava.New(cfg.WatchAddr, mcjava.DefaultTimeout)
	healthServer := health.New()
	machine := lifecycle.New(lifecycle.Deps{
		Discoverer: awsx.NewDiscoverer(
			platform.New(getenv("ECS_CONTAINER_METADATA_URI_V4"), &http.Client{Timeout: metadataTimeout}),
			ecsClient, ec2.NewFromConfig(awsCfg), cfg.Cluster),
		Gate:     awsx.NewGate(ecsClient, cfg.Cluster, cfg.Service),
		DNS:      awsx.NewDNS(route53.NewFromConfig(awsCfg), cfg.DNSZoneID, cfg.DNSRecord, cfg.DNSTTL),
		Reaper:   awsx.NewReaper(ecsClient, cfg.Cluster, cfg.Service),
		Notifier: awsx.NewNotifier(sns.NewFromConfig(awsCfg), cfg.SNSTopicARN, cfg.Cluster, cfg.Service),
		Watcher:  prober,
		Health:   healthServer,
		Logger:   logger,
	}, settings(cfg))

	if proberErr != nil {
		logger.ErrorContext(ctx, "invalid watch address", slog.Any("error", proberErr))
		return machine.Abort("invalid watch address")
	}
	if err = healthServer.Listen(net.JoinHostPort(loopback, strconv.Itoa(cfg.HealthPort))); err != nil {
		logger.ErrorContext(ctx, "health endpoint unavailable", slog.Any("error", err))
		return machine.Abort("health endpoint unavailable")
	}
	go func() {
		if serr := healthServer.Serve(); serr != nil {
			logger.ErrorContext(ctx, "health endpoint stopped", slog.Any("error", serr))
		}
	}()
	defer func() { _ = healthServer.Close() }()

	logger.InfoContext(ctx, "custodian starting",
		slog.String("cluster", cfg.Cluster), slog.String("service", cfg.Service),
		slog.String("record", cfg.DNSRecord), slog.String("watch", cfg.WatchAddr))
	return machine.Run(ctx)
}

func newLogger(out io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With(slog.String("version", version))
}

func settings(cfg config.Config) lifecycle.Settings {
	s := lifecycle.DefaultSettings()
	s.ParkedIP = cfg.DNSParkedIP
	s.WatchInterval = cfg.WatchInterval
	s.IdleTimeout = cfg.IdleTimeout
	s.BootTimeout = cfg.BootTimeout
	s.GateTimeout = cfg.GateTimeout
	s.ProbeFailWarn = cfg.ProbeFailWarn
	return s
}
