package vitekit

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// teardownTimeout bounds waits for a process tree to come down. Teardown may
// spend StopGracePeriod asking politely before it kills, then wait again to
// confirm the tree is gone, and a loaded CI runner is slower than either — so a
// test that allows exactly StopGracePeriod is racing its own subject.
const teardownTimeout = StopGracePeriod + killConfirmPeriod + 20*time.Second

func TestStartDevAndWait(t *testing.T) {
	// "echo hello" is understood by both cmd.exe and sh.
	cmd := "echo hello"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	proc, err := StartDev(ctx, cmd, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("process exited with error: %v", err)
	}
	if got := bytes.TrimSpace(stdout.Bytes()); string(got) != "hello" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	// Use a command that stays alive long enough to be killed.
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 60 127.0.0.1"
	} else {
		cmd = "sleep 60"
	}

	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	proc, err := StartDev(ctx, cmd, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Stop(); err != nil {
		t.Fatalf("first Stop() failed: %v", err)
	}
	// Second call must not panic or error.
	if err := proc.Stop(); err != nil {
		t.Fatalf("second Stop() failed: %v", err)
	}
}

func TestContextCancellationKillsProcess(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 60 127.0.0.1"
	} else {
		cmd = "sleep 60"
	}

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	proc, err := StartDev(ctx, cmd, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}

	cancel() // should kill the child

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	select {
	case <-done:
		// Process exited — success (exit error is expected after kill).
	case <-time.After(teardownTimeout):
		t.Fatal("process did not exit after context cancellation")
	}
}

func TestDevOrchestratorRunsAndCleansUp(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 60 127.0.0.1"
	} else {
		cmd = "sleep 60"
	}

	var stdout, stderr bytes.Buffer
	orch := NewDevOrchestrator(cmd, func(o *DevOrchestrator) {
		o.Stdout = &stdout
		o.Stderr = &stderr
	})

	ctx, cancel := context.WithCancel(context.Background())
	backendStarted := make(chan struct{})

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = orch.Run(ctx, func(bCtx context.Context) error {
			close(backendStarted)
			<-bCtx.Done()
			return bCtx.Err()
		})
	}()

	select {
	case <-backendStarted:
		// Backend successfully booted concurrently with dev server
	case <-time.After(teardownTimeout):
		t.Fatal("backend boot was not triggered")
	}

	// Cancel context to simulate Ctrl+C signal
	cancel()

	// Wait for the teardown this test is named for, rather than leaving a dev
	// server running behind the rest of the suite.
	select {
	case <-finished:
	case <-time.After(teardownTimeout):
		t.Fatal("orchestrator did not shut down")
	}
}

func TestDevOrchestratorWritesHotFile(t *testing.T) {
	tempDir := t.TempDir()
	hotFile := filepath.Join(tempDir, ".vite", "hot")

	// Echo simulated Vite output
	cmd := "echo Local: http://localhost:5173/ && echo Network: use --host"

	orch := NewDevOrchestrator(cmd, func(o *DevOrchestrator) {
		o.Dir = tempDir
		o.HotFilePath = ".vite/hot"
		o.Stdout = &bytes.Buffer{}
		o.Stderr = &bytes.Buffer{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Run(ctx, func(bCtx context.Context) error {
		// Wait for hot file to be written by orchestrator
		for i := 0; i < 50; i++ {
			if data, err := os.ReadFile(hotFile); err == nil {
				if string(bytes.TrimSpace(data)) == "http://localhost:5173" {
					return nil // Success!
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		return fmt.Errorf("hot file not written in time")
	})

	if err != nil {
		t.Fatalf("orchestrator run failed: %v", err)
	}

	// Hot file should be cleaned up after run exits
	if _, err := os.Stat(hotFile); !os.IsNotExist(err) {
		t.Errorf("expected hot file to be removed, but stat error = %v", err)
	}
}

func TestViteOutputWriterHidesLocalURL(t *testing.T) {
	var output bytes.Buffer
	writer := &viteOutputWriter{target: &output}
	if _, err := writer.Write([]byte("Local: http://localhost:5173/\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "http://localhost:5173") {
		t.Fatalf("local URL was exposed in terminal output: %q", output.String())
	}
	if !strings.Contains(output.String(), "Vite development server running") {
		t.Fatalf("replacement status was not written: %q", output.String())
	}
}

func TestDevOrchestratorRemovesStaleHotFile(t *testing.T) {
	tempDir := t.TempDir()
	hotFile := filepath.Join(tempDir, ".vite", "hot")
	if err := os.MkdirAll(filepath.Dir(hotFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hotFile, []byte("http://localhost:5999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var command string
	if runtime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1"
	} else {
		command = "sleep 60"
	}
	orchestrator := NewDevOrchestrator(command, func(options *DevOrchestrator) {
		options.Dir = tempDir
		options.HotFilePath = ".vite/hot"
		options.Stdout = &bytes.Buffer{}
		options.Stderr = &bytes.Buffer{}
	})

	runContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = orchestrator.Run(runContext, func(runContext context.Context) error {
			if _, err := os.Stat(hotFile); !os.IsNotExist(err) {
				return fmt.Errorf("stale hot file still exists: %w", err)
			}
			close(started)
			<-runContext.Done()
			return runContext.Err()
		})
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(teardownTimeout):
		t.Fatal("orchestrator did not start")
	}

	// t.TempDir removes the directory next, and Windows will not delete one
	// that is still a live process's working directory. Wait for the tree to
	// actually be gone rather than merely asked to go.
	select {
	case <-finished:
	case <-time.After(teardownTimeout):
		t.Fatal("orchestrator did not shut down")
	}
}

func TestCommandWithVitePort(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		// Script runners swallow trailing flags unless they follow "--".
		{"npm", "npm run dev", "npm run dev -- --host 127.0.0.1 --port 5180"},
		{"bun", "bun run dev", "bun run dev -- --host 127.0.0.1 --port 5180"},
		{"pnpm run", "pnpm run dev", "pnpm run dev -- --host 127.0.0.1 --port 5180"},
		// Direct invocations take Vite's flags as their own.
		{"pnpm", "pnpm dev", "pnpm dev --host 127.0.0.1 --port 5180"},
		{"yarn", "yarn dev", "yarn dev --host 127.0.0.1 --port 5180"},
		{"npx", "npx vite", "npx vite --host 127.0.0.1 --port 5180"},
		{"bare vite", "vite", "vite --host 127.0.0.1 --port 5180"},
		// An explicitly pinned port is the operator's choice and must survive.
		{"explicit port", "npm run dev -- --port 4000", "npm run dev -- --port 4000"},
		{"explicit port=", "vite --port=4000", "vite --port=4000"},
		// Nothing recognizable to append to.
		{"unknown runner", "make frontend", "make frontend"},
		{"empty", "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := commandWithVitePort(testCase.command, 5180); got != testCase.want {
				t.Fatalf("commandWithVitePort(%q) = %q, want %q", testCase.command, got, testCase.want)
			}
		})
	}
}

// safeBuffer serializes the writes the output relay goroutines make against
// the reads the test performs.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestDevOrchestratorStreamsDevServerOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell quoting")
	}
	var stdout, stderr safeBuffer
	// Two things must happen: ordinary Vite output reaches the terminal, and
	// the banner advertising Vite's own URL is rewritten so nobody bypasses
	// the Go backend by clicking it.
	command := "echo 'vite v5.0.0 ready'; echo '  ➜  Local: http://localhost:5173/'; echo 'plugin failure' 1>&2"

	orch := NewDevOrchestrator(command, func(o *DevOrchestrator) {
		o.Dir = t.TempDir()
		o.Stdout = &stdout
		o.Stderr = &stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := orch.Run(ctx, nil); err != nil {
		t.Fatalf("orchestrator run failed: %v", err)
	}

	if !strings.Contains(stdout.String(), "vite v5.0.0 ready") {
		t.Errorf("dev-server stdout was not forwarded: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "plugin failure") {
		t.Errorf("dev-server stderr was not forwarded: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "http://localhost:5173") {
		t.Errorf("Vite's own URL leaked to the terminal: %q", stdout.String())
	}
}

func TestFindAvailableVitePortSupportsPortsBelowDefault(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:3000")
	if err != nil {
		t.Skipf("port 3000 is unavailable: %v", err)
	}
	defer func() { _ = listener.Close() }()
	if got := findAvailableVitePort(3000); got != 3001 {
		t.Fatalf("findAvailableVitePort(3000) = %d, want 3001", got)
	}
}

// Stop must not sit through the grace period waiting for a shutdown that was
// never going to happen. This is a real hazard on Windows, where the obvious
// "ask politely" primitive (taskkill without /F) posts WM_CLOSE, which console
// applications like node ignore while reporting success — every Ctrl+C would
// then stall for the full StopGracePeriod before killing the tree anyway.
func TestStopReturnsWellWithinTheGracePeriod(t *testing.T) {
	var command string
	if runtime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1"
	} else {
		command = "sleep 60"
	}

	process, err := StartDev(context.Background(), command, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Stop() })

	start := time.Now()
	if err := process.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	elapsed := time.Since(start)

	// Generous enough for a loaded CI runner, tight enough to catch a full
	// grace-period stall.
	limit := StopGracePeriod / 2
	if elapsed >= limit {
		t.Errorf("Stop() took %v, want well under the %v grace period", elapsed, StopGracePeriod)
	}
	t.Logf("Stop() returned in %v (grace period is %v)", elapsed, StopGracePeriod)
}

func TestCheckDevCommand(t *testing.T) {
	// A program that certainly exists, and one that certainly does not.
	present := "sh"
	if runtime.GOOS == "windows" {
		present = "cmd"
	}

	cases := []struct {
		name      string
		command   string
		wantError bool
	}{
		{"present program", present + " -c true", false},
		{"missing program", "definitely-not-installed-xyz run dev", true},
		{"empty command", "", false},
		// Shell syntax is the shell's to resolve, so it is left alone rather
		// than being wrongly reported as a missing program.
		{"env assignment", "FOO=bar npm run dev", false},
		{"subshell", "$(which npm) run dev", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := checkDevCommand(testCase.command)
			if testCase.wantError && err == nil {
				t.Fatal("expected an error")
			}
			if !testCase.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// The message has to name the missing program and say what to do about it —
// "exit status 127" is what this replaces.
func TestCheckDevCommandMessageIsActionable(t *testing.T) {
	err := checkDevCommand("npm run dev")
	if err == nil {
		t.Skip("npm is installed here, so there is no failure to inspect")
	}
	for _, want := range []string{"npm", "PATH", "nodejs.org", "--cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message is missing %q: %v", want, err)
		}
	}
}

// A dev server that cannot start must fail immediately rather than being
// reported as a normal shutdown.
func TestDevOrchestratorRefusesAMissingDevCommand(t *testing.T) {
	orch := NewDevOrchestrator("definitely-not-installed-xyz dev", func(o *DevOrchestrator) {
		o.Dir = t.TempDir()
		o.Stdout = &bytes.Buffer{}
		o.Stderr = &bytes.Buffer{}
	})

	booted := false
	err := orch.Run(context.Background(), func(context.Context) error {
		booted = true
		return nil
	})
	if err == nil {
		t.Fatal("expected an error for a dev command that does not exist")
	}
	if booted {
		t.Error("the backend was started even though the dev server could not be")
	}
	if !strings.Contains(err.Error(), "definitely-not-installed-xyz") {
		t.Errorf("error does not name the missing program: %v", err)
	}
}
