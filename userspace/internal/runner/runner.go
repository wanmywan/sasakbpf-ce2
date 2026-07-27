package runner

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// Result captures the outcome of a shell command execution.
type Result struct {
	Cmd    string
	Stdout string
	Stderr string
	Err    error
	Dur    time.Duration
}

// Exec runs cmd via /bin/sh -c "<cmd>" with a timeout.
// Implemented fully in Fase 4.
func Exec(ctx context.Context, cmd string, timeout time.Duration) Result {
	t := timeout
	if t == 0 {
		t = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()

	start := time.Now()
	c := exec.CommandContext(cctx, "/bin/sh", "-c", cmd)
	var sout, serr bytes.Buffer
	c.Stdout = &sout
	c.Stderr = &serr
	err := c.Run()
	r := Result{
		Cmd:    cmd,
		Stdout: sout.String(),
		Stderr: serr.String(),
		Err:    err,
		Dur:    time.Since(start),
	}
	if r.Stderr == "" && r.Err != nil {
		r.Stderr = err.Error()
		if !strings.HasSuffix(r.Stderr, "\n") {
			r.Stderr += "\n"
		}
	}
	return r
}