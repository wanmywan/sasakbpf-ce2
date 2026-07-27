//go:build !obfuscate

package config

var (
	xorKey       []byte
	xorBotToken  []byte
	xorChannelID []byte
	xorTargetID  []byte
	xorAESKeyHex []byte
)
