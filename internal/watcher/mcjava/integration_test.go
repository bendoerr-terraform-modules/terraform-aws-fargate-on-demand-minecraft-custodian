//go:build integration

package mcjava_test

import (
	"os"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/watcher/mcjava"
)

// TestProbeAgainstPaper expects a Paper server that was *just* started at CUSTODIAN_IT_MC_ADDR.
func TestProbeAgainstPaper(t *testing.T) {
	addr := os.Getenv("CUSTODIAN_IT_MC_ADDR")
	if addr == "" {
		t.Skip("CUSTODIAN_IT_MC_ADDR not set")
	}
	p, err := mcjava.New(addr, mcjava.DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Minute)
	sawFailure := false
	for {
		n, perr := p.Probe(t.Context())
		if perr == nil {
			if !sawFailure {
				t.Fatal("first probe succeeded; want at least one failure while Paper boots")
			}
			if n != 0 {
				t.Fatalf("players online = %d; want 0 on a fresh server", n)
			}
			t.Logf("Paper ready; players online = %d", n)
			return
		}
		sawFailure = true
		if time.Now().After(deadline) {
			t.Fatalf("Paper not ready by deadline; last error: %v", perr)
		}
		time.Sleep(5 * time.Second)
	}
}
