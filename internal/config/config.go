// Package config loads and validates the custodian's environment configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// Environment variable names (spec §5).
const (
	EnvCluster       = "CUSTODIAN_CLUSTER"
	EnvService       = "CUSTODIAN_SERVICE"
	EnvDNSZoneID     = "CUSTODIAN_DNS_ZONE_ID"
	EnvDNSRecord     = "CUSTODIAN_DNS_RECORD"
	EnvDNSTTL        = "CUSTODIAN_DNS_TTL"
	EnvDNSParkedIP   = "CUSTODIAN_DNS_PARKED_IP"
	EnvSNSTopicARN   = "CUSTODIAN_SNS_TOPIC_ARN"
	EnvWatchAddr     = "CUSTODIAN_WATCH_ADDR"
	EnvWatchInterval = "CUSTODIAN_WATCH_INTERVAL"
	EnvIdleTimeout   = "CUSTODIAN_IDLE_TIMEOUT"
	EnvBootTimeout   = "CUSTODIAN_BOOT_TIMEOUT"
	EnvGateTimeout   = "CUSTODIAN_GATE_TIMEOUT"
	EnvProbeFailWarn = "CUSTODIAN_PROBE_FAIL_WARN"
	EnvHealthPort    = "CUSTODIAN_HEALTH_PORT"
	EnvLogLevel      = "CUSTODIAN_LOG_LEVEL"
)

// MaxGateTimeout keeps the gate inside the ECS health-check start period, which ECS caps at 300 s (spec §3.3).
const MaxGateTimeout = 4 * time.Minute

const (
	defaultDNSTTL        = 30
	maxDNSTTL            = 86400
	defaultWatchAddr     = "127.0.0.1:25565"
	defaultWatchInterval = 30 * time.Second
	defaultIdleTimeout   = 10 * time.Minute
	defaultBootTimeout   = 10 * time.Minute
	defaultGateTimeout   = 3 * time.Minute
	defaultProbeFailWarn = 3
	maxProbeFailWarn     = 1000
	defaultHealthPort    = 8558
	maxPort              = 65535
)

// Config is the validated custodian configuration.
type Config struct {
	Cluster       string
	Service       string
	DNSZoneID     string
	DNSRecord     string
	DNSTTL        int64
	DNSParkedIP   string
	SNSTopicARN   string
	WatchAddr     string
	WatchInterval time.Duration
	IdleTimeout   time.Duration
	BootTimeout   time.Duration
	GateTimeout   time.Duration
	// ProbeFailWarn only selects the log level: after this many consecutive probe failures they log at error.
	// It is not a failure tolerance; every failed probe already counts as 0 players.
	ProbeFailWarn int
	HealthPort    int
	LogLevel      slog.Level
}

// Load reads the configuration through getenv and reports every invalid variable at once.
func Load(getenv func(string) string) (Config, error) {
	p := &parser{getenv: getenv}
	cfg := Config{
		Cluster:       p.required(EnvCluster),
		Service:       p.required(EnvService),
		DNSZoneID:     p.required(EnvDNSZoneID),
		DNSRecord:     p.required(EnvDNSRecord),
		DNSTTL:        int64(p.intInRange(EnvDNSTTL, defaultDNSTTL, maxDNSTTL)),
		DNSParkedIP:   p.optionalIPv4(EnvDNSParkedIP),
		SNSTopicARN:   getenv(EnvSNSTopicARN),
		WatchAddr:     p.hostPort(EnvWatchAddr, defaultWatchAddr),
		WatchInterval: p.positiveDuration(EnvWatchInterval, defaultWatchInterval),
		IdleTimeout:   p.positiveDuration(EnvIdleTimeout, defaultIdleTimeout),
		BootTimeout:   p.positiveDuration(EnvBootTimeout, defaultBootTimeout),
		GateTimeout:   p.positiveDuration(EnvGateTimeout, defaultGateTimeout),
		ProbeFailWarn: p.intInRange(EnvProbeFailWarn, defaultProbeFailWarn, maxProbeFailWarn),
		HealthPort:    p.intInRange(EnvHealthPort, defaultHealthPort, maxPort),
		LogLevel:      p.logLevel(EnvLogLevel, slog.LevelInfo),
	}
	if cfg.IdleTimeout <= cfg.WatchInterval {
		p.fail(EnvIdleTimeout, fmt.Sprintf("must be greater than %s (%s)", EnvWatchInterval, cfg.WatchInterval))
	}
	if cfg.GateTimeout > MaxGateTimeout {
		p.fail(EnvGateTimeout, "must be at most "+MaxGateTimeout.String())
	}
	return cfg, p.err()
}

// HealthPort reads only the health port, for the `healthcheck` subcommand.
func HealthPort(getenv func(string) string) (int, error) {
	p := &parser{getenv: getenv}
	port := p.intInRange(EnvHealthPort, defaultHealthPort, maxPort)
	return port, p.err()
}

type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) fail(name, msg string) {
	p.errs = append(p.errs, fmt.Errorf("%s: %s", name, msg))
}

func (p *parser) err() error {
	return errors.Join(p.errs...)
}

func (p *parser) required(name string) string {
	v := p.getenv(name)
	if v == "" {
		p.fail(name, "is required")
	}
	return v
}

// intInRange parses an integer between 1 and hi inclusive.
func (p *parser) intInRange(name string, def, hi int) int {
	const lo = 1
	v := p.getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		p.fail(name, fmt.Sprintf("must be an integer between %d and %d, got %q", lo, hi, v))
		return def
	}
	return n
}

func (p *parser) positiveDuration(name string, def time.Duration) time.Duration {
	v := p.getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.fail(name, fmt.Sprintf("must be a positive duration such as 30s or 10m, got %q", v))
		return def
	}
	return d
}

func (p *parser) optionalIPv4(name string) string {
	v := p.getenv(name)
	if v == "" {
		return ""
	}
	addr, err := netip.ParseAddr(v)
	if err != nil || !addr.Is4() {
		p.fail(name, fmt.Sprintf("must be an IPv4 address, got %q", v))
		return ""
	}
	return addr.String()
}

func (p *parser) hostPort(name, def string) string {
	v := p.getenv(name)
	if v == "" {
		return def
	}
	host, port, err := net.SplitHostPort(v)
	n, perr := strconv.Atoi(port)
	if err != nil || perr != nil || host == "" || n < 1 || n > maxPort {
		p.fail(name, fmt.Sprintf("must be host:port with a port between 1 and %d, got %q", maxPort, v))
		return def
	}
	return v
}

func (p *parser) logLevel(name string, def slog.Level) slog.Level {
	v := p.getenv(name)
	if v == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		p.fail(name, fmt.Sprintf("must be one of debug, info, warn, error, got %q", v))
		return def
	}
	return lvl
}
