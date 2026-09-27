package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/config"
)

func env(overrides map[string]string) func(string) string {
	vars := map[string]string{
		"CUSTODIAN_CLUSTER":     "shanecraft",
		"CUSTODIAN_SERVICE":     "minecraft",
		"CUSTODIAN_DNS_ZONE_ID": "Z0123456789ABC",
		"CUSTODIAN_DNS_RECORD":  "mc.example.com",
	}
	for k, v := range overrides {
		vars[k] = v
	}
	return func(k string) string { return vars[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := config.Config{
		Cluster:       "shanecraft",
		Service:       "minecraft",
		DNSZoneID:     "Z0123456789ABC",
		DNSRecord:     "mc.example.com",
		DNSTTL:        30,
		WatchAddr:     "127.0.0.1:25565",
		WatchInterval: 30 * time.Second,
		IdleTimeout:   10 * time.Minute,
		BootTimeout:   10 * time.Minute,
		GateTimeout:   3 * time.Minute,
		ProbeFailWarn: 3,
		HealthPort:    8558,
		LogLevel:      slog.LevelInfo,
	}
	if cfg != want {
		t.Errorf("Load() =\n%+v\nwant\n%+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"CUSTODIAN_DNS_TTL":         "60",
		"CUSTODIAN_DNS_PARKED_IP":   "192.0.2.1",
		"CUSTODIAN_SNS_TOPIC_ARN":   "arn:aws:sns:us-east-1:123456789012:events",
		"CUSTODIAN_WATCH_ADDR":      "localhost:25566",
		"CUSTODIAN_WATCH_INTERVAL":  "15s",
		"CUSTODIAN_IDLE_TIMEOUT":    "20m",
		"CUSTODIAN_BOOT_TIMEOUT":    "5m",
		"CUSTODIAN_GATE_TIMEOUT":    "4m",
		"CUSTODIAN_PROBE_FAIL_WARN": "5",
		"CUSTODIAN_HEALTH_PORT":     "9000",
		"CUSTODIAN_LOG_LEVEL":       "debug",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DNSTTL != 60 || cfg.DNSParkedIP != "192.0.2.1" ||
		cfg.SNSTopicARN != "arn:aws:sns:us-east-1:123456789012:events" || cfg.WatchAddr != "localhost:25566" ||
		cfg.WatchInterval != 15*time.Second || cfg.IdleTimeout != 20*time.Minute ||
		cfg.BootTimeout != 5*time.Minute || cfg.GateTimeout != 4*time.Minute ||
		cfg.ProbeFailWarn != 5 || cfg.HealthPort != 9000 || cfg.LogLevel != slog.LevelDebug {
		t.Errorf("Load() did not apply overrides: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantVar string
	}{
		{"missing cluster", map[string]string{"CUSTODIAN_CLUSTER": ""}, "CUSTODIAN_CLUSTER"},
		{"missing service", map[string]string{"CUSTODIAN_SERVICE": ""}, "CUSTODIAN_SERVICE"},
		{"missing zone", map[string]string{"CUSTODIAN_DNS_ZONE_ID": ""}, "CUSTODIAN_DNS_ZONE_ID"},
		{"missing record", map[string]string{"CUSTODIAN_DNS_RECORD": ""}, "CUSTODIAN_DNS_RECORD"},
		{"ttl zero", map[string]string{"CUSTODIAN_DNS_TTL": "0"}, "CUSTODIAN_DNS_TTL"},
		{"ttl not a number", map[string]string{"CUSTODIAN_DNS_TTL": "abc"}, "CUSTODIAN_DNS_TTL"},
		{"ttl too large", map[string]string{"CUSTODIAN_DNS_TTL": "86401"}, "CUSTODIAN_DNS_TTL"},
		{"parked ip garbage", map[string]string{"CUSTODIAN_DNS_PARKED_IP": "not-an-ip"}, "CUSTODIAN_DNS_PARKED_IP"},
		{"parked ip v6", map[string]string{"CUSTODIAN_DNS_PARKED_IP": "2001:db8::1"}, "CUSTODIAN_DNS_PARKED_IP"},
		{"watch addr no port", map[string]string{"CUSTODIAN_WATCH_ADDR": "localhost"}, "CUSTODIAN_WATCH_ADDR"},
		{"watch addr port zero", map[string]string{"CUSTODIAN_WATCH_ADDR": "localhost:0"}, "CUSTODIAN_WATCH_ADDR"},
		{"watch addr port too big", map[string]string{"CUSTODIAN_WATCH_ADDR": "localhost:70000"}, "CUSTODIAN_WATCH_ADDR"},
		{"watch addr empty host", map[string]string{"CUSTODIAN_WATCH_ADDR": ":25565"}, "CUSTODIAN_WATCH_ADDR"},
		{"interval zero", map[string]string{"CUSTODIAN_WATCH_INTERVAL": "0s"}, "CUSTODIAN_WATCH_INTERVAL"},
		{"interval negative", map[string]string{"CUSTODIAN_WATCH_INTERVAL": "-5s"}, "CUSTODIAN_WATCH_INTERVAL"},
		{"interval garbage", map[string]string{"CUSTODIAN_WATCH_INTERVAL": "soon"}, "CUSTODIAN_WATCH_INTERVAL"},
		{
			"idle not greater than interval",
			map[string]string{"CUSTODIAN_WATCH_INTERVAL": "1m", "CUSTODIAN_IDLE_TIMEOUT": "1m"},
			"CUSTODIAN_IDLE_TIMEOUT",
		},
		{"boot zero", map[string]string{"CUSTODIAN_BOOT_TIMEOUT": "0s"}, "CUSTODIAN_BOOT_TIMEOUT"},
		{"gate above max", map[string]string{"CUSTODIAN_GATE_TIMEOUT": "5m"}, "CUSTODIAN_GATE_TIMEOUT"},
		{"probe fail warn zero", map[string]string{"CUSTODIAN_PROBE_FAIL_WARN": "0"}, "CUSTODIAN_PROBE_FAIL_WARN"},
		{"health port zero", map[string]string{"CUSTODIAN_HEALTH_PORT": "0"}, "CUSTODIAN_HEALTH_PORT"},
		{"health port too big", map[string]string{"CUSTODIAN_HEALTH_PORT": "65536"}, "CUSTODIAN_HEALTH_PORT"},
		{"log level unknown", map[string]string{"CUSTODIAN_LOG_LEVEL": "loud"}, "CUSTODIAN_LOG_LEVEL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(env(tt.env))
			if err == nil {
				t.Fatalf("Load() error = nil, want error mentioning %s", tt.wantVar)
			}
			if !strings.Contains(err.Error(), tt.wantVar) {
				t.Errorf("Load() error = %q, want it to mention %s", err, tt.wantVar)
			}
		})
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := config.Load(env(map[string]string{"CUSTODIAN_CLUSTER": "", "CUSTODIAN_DNS_TTL": "abc"}))
	if err == nil {
		t.Fatal("Load() error = nil, want errors")
	}
	for _, name := range []string{"CUSTODIAN_CLUSTER", "CUSTODIAN_DNS_TTL"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Load() error = %q, want it to mention %s", err, name)
		}
	}
}

func TestHealthPort(t *testing.T) {
	port, err := config.HealthPort(func(string) string { return "" })
	if err != nil || port != 8558 {
		t.Errorf("HealthPort(default) = %d, %v; want 8558, nil", port, err)
	}
	port, err = config.HealthPort(env(map[string]string{"CUSTODIAN_HEALTH_PORT": "9000"}))
	if err != nil || port != 9000 {
		t.Errorf("HealthPort(9000) = %d, %v; want 9000, nil", port, err)
	}
	if _, err = config.HealthPort(env(map[string]string{"CUSTODIAN_HEALTH_PORT": "x"})); err == nil {
		t.Error("HealthPort(x) error = nil, want error")
	}
}
