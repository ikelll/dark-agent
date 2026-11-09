package xray

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkerline/agent/internal/config"
)

func TestExitedChildIsNotRunning(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 0.4\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := NewProcess(&config.Config{XrayBin: bin})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child was not reaped")
	}
	if p.IsRunning() || p.Uptime() != 0 {
		t.Fatal("exited child reported healthy")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRestartRejectsInvalidConfigBeforeStopping(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$2\" = \"-test\" ]; then exit 1; fi\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := NewProcess(&config.Config{XrayBin: bin})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	pid := p.cmd.Process.Pid
	if err := p.Restart(); err == nil {
		t.Fatal("accepted invalid config")
	}
	if !p.IsRunning() || p.cmd.Process.Pid != pid {
		t.Fatal("stopped healthy process before validation")
	}
}
