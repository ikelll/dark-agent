package xray

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/darkerline/agent/internal/config"
)

// Process manages the Xray subprocess started by the agent.
type Process struct {
	mu      sync.Mutex
	cfg     *config.Config
	cmd     *exec.Cmd
	started time.Time
}

func NewProcess(cfg *config.Config) *Process {
	return &Process{cfg: cfg}
}

func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.runningLocked() {
		return fmt.Errorf("xray already running")
	}

	cmd := exec.Command(p.cfg.XrayBin, "run", "-c", p.cfg.XrayConfig)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xray: %w", err)
	}

	p.cmd = cmd
	p.started = time.Now()
	return nil
}

func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.runningLocked() {
		p.cmd = nil
		p.started = time.Time{}
		return nil
	}

	cmd := p.cmd
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil && err != syscall.ESRCH {
			return fmt.Errorf("stop xray: %w", err)
		}
	}

	// The process may already be reaped or return an exit status after SIGTERM;
	// both are normal during a controlled restart.
	_ = cmd.Wait()
	p.cmd = nil
	p.started = time.Time{}
	return nil
}

// Restart safely applies an updated config by starting a fresh Xray process.
func (p *Process) Restart() error {
	if err := p.Stop(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return p.Start()
}

// Reload applies configuration changes. Xray config files are applied through a
// controlled restart rather than by sending an unchecked Unix signal.
func (p *Process) Reload() error {
	return p.Restart()
}

func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runningLocked()
}

func (p *Process) runningLocked() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if err := p.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		p.cmd = nil
		p.started = time.Time{}
		return false
	}
	return true
}

func (p *Process) Uptime() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.runningLocked() {
		return 0
	}
	return time.Since(p.started)
}

// Version returns xray --version output.
func (p *Process) Version(bin string) string {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "unknown"
	}
	for i, b := range out {
		if b == '\n' {
			return string(out[:i])
		}
	}
	return string(out)
}
