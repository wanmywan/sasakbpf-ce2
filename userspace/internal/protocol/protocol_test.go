package protocol

import (
	"strings"
	"testing"
)

func TestEncodeCmdDecode(t *testing.T) {
	key := strings.Repeat("aa", 32) // 32-byte key
	cmd := "exec whoami"
	target := "abc123"
	line, err := Encode(PrefixCmd, target, key, []byte(cmd))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(line, "sd1:abc123:") {
		t.Fatalf("bad line: %q", line)
	}
	msg, err := Decode(line, target, key)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Kind != KindCmd {
		t.Fatalf("kind=%v want cmd", msg.Kind)
	}
	if msg.TargetID != target {
		t.Fatalf("target=%q want %q", msg.TargetID, target)
	}
	if string(msg.Body) != cmd {
		t.Fatalf("body=%q want %q", msg.Body, cmd)
	}
}

func TestEncodeOutDecode(t *testing.T) {
	key := strings.Repeat("bb", 32)
	out := "uid=0(root)"
	target := "feedface"
	line, err := Encode(PrefixOut, target, key, []byte(out))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(line, "sd1ack:feedface:") {
		t.Fatalf("bad line: %q", line)
	}
	// Agent-targeted decode must mark as KindOut
	msg, err := Decode(line, target, key)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Kind != KindOut {
		t.Fatalf("kind=%v want out", msg.Kind)
	}
	if string(msg.Body) != out {
		t.Fatalf("body=%q want %q", msg.Body, out)
	}
}

func TestDecodeWrongTargetSkipped(t *testing.T) {
	key := strings.Repeat("cc", 32)
	line, _ := Encode(PrefixCmd, "alpha", key, []byte("cmd"))
	msg, err := Decode(line, "beta", key)
	if err == nil && msg != nil {
		t.Fatalf("expected ErrNotForUs, got msg %+v", msg)
	}
	if err != nil && err != ErrNotForUs {
		t.Fatalf("err=%v want ErrNotForUs", err)
	}
}

func TestDecodeCliAcceptsAnyTarget(t *testing.T) {
	key := strings.Repeat("dd", 32)
	line, _ := Encode(PrefixOut, "anytid", key, []byte("x"))
	// targetID="" means CLI listening to all targets
	msg, err := Decode(line, "", key)
	if err != nil {
		t.Fatalf("cli decode: %v", err)
	}
	if msg.TargetID != "anytid" {
		t.Fatalf("tid=%q", msg.TargetID)
	}
}

func TestPingPongPlain(t *testing.T) {
	m, err := Decode("sd1ping:abc", "", "")
	if err != nil {
		t.Fatalf("decode ping: %v", err)
	}
	if m.Kind != KindPing {
		t.Fatalf("kind=%v want ping", m.Kind)
	}
	if m.TargetID != "abc" {
		t.Fatalf("tid=%q", m.TargetID)
	}
	if string(m.Body) != "" {
		t.Fatalf("body should be empty")
	}

	m, err = Decode("sd1pong:def", "", "")
	if err != nil {
		t.Fatalf("decode pong: %v", err)
	}
	if m.Kind != KindPong {
		t.Fatalf("kind=%v want pong", m.Kind)
	}
	if m.TargetID != "def" {
		t.Fatalf("tid=%q", m.TargetID)
	}
}

func TestNonMatchingLine(t *testing.T) {
	if _, err := Decode("hello how are you", "", ""); err == nil {
		t.Fatalf("non-matching line should error")
	}
}

// Tamper test: half-flip last byte of a valid line — should fail AES-GCM.
func TestTamperedCiphertextFails(t *testing.T) {
	key := strings.Repeat("ee", 32)
	line, _ := Encode(PrefixCmd, "tid1", key, []byte("secret cmd"))
	b := []byte(line)
	b[len(b)-2] = byte(int(b[len(b)-2]) ^ 0x55) // arbitrary bit-flip
	if _, err := Decode(string(b), "tid1", key); err == nil {
		t.Fatalf("decode of tampered line succeeded — GCM broken")
	}
}