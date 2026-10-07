//go:build !windows

package vitekit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startForkingDevServer launches a shell that backgrounds a long-lived child
// and records its PID. That grandchild models what "npm run dev" really does:
// npm forks node, so a signal aimed only at the process we started leaves Vite
// running, holding its port and the output pipes it inherited.
func startForkingDevServer(t *testing.T) (*DevProcess, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf("sleep 60 & echo $! > %s; wait", pidFile)

	process, err := StartDev(context.Background(), command, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Stop() })

	var grandchild int
	for i := 0; i < 100; i++ {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				grandchild = pid
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grandchild == 0 {
		t.Fatal("dev server never reported its child PID")
	}
	if !processAlive(grandchild) {
		t.Fatalf("child %d was not running at the start of the test", grandchild)
	}
	return process, grandchild
}

func processAlive(pid int) bool {
	// Signal 0 performs the permission and existence checks without delivering
	// anything, which is the standard liveness probe.
	return syscall.Kill(pid, 0) == nil
}

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !processAlive(pid)
}

func TestStopTerminatesTheWholeProcessTree(t *testing.T) {
	process, grandchild := startForkingDevServer(t)

	if err := process.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if !waitForExit(grandchild, 10*time.Second) {
		t.Fatalf("child %d survived Stop(); the dev server was orphaned", grandchild)
	}
	if err := process.Wait(); err == nil {
		t.Log("dev server exited cleanly")
	}
}

func TestContextCancellationTerminatesTheWholeProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf("sleep 60 & echo $! > %s; wait", pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	process, err := StartDev(ctx, command, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); _ = process.Stop() }()

	var grandchild int
	for i := 0; i < 100 && grandchild == 0; i++ {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				grandchild = pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grandchild == 0 {
		t.Fatal("dev server never reported its child PID")
	}

	cancel()

	if !waitForExit(grandchild, 10*time.Second) {
		t.Fatalf("child %d survived context cancellation", grandchild)
	}
}

// The orchestrator must never signal the process group it is running in — that
// would take down the caller (and, in CI, the test binary itself) along with
// the dev server.
func TestStopLeavesTheCallersProcessGroupAlone(t *testing.T) {
	process, _ := startForkingDevServer(t)

	ourGroup, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("could not read our own process group: %v", err)
	}
	childGroup, err := syscall.Getpgid(process.command.Process.Pid)
	if err != nil {
		t.Fatalf("could not read the dev server's process group: %v", err)
	}
	if childGroup == ourGroup {
		t.Fatalf("dev server shares our process group (%d); signaling it would kill the caller", ourGroup)
	}

	if err := process.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if !processAlive(os.Getpid()) {
		t.Fatal("the test process was signaled by Stop()")
	}
}
