package vitekit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// StopGracePeriod is how long the dev server is given to exit on its own
	// after being asked to stop, before it is killed outright. Vite uses this
	// window to release its TCP port.
	StopGracePeriod = 5 * time.Second

	// killConfirmPeriod bounds how long Stop waits for a force-killed process
	// tree to actually disappear before giving up on confirming it.
	killConfirmPeriod = 5 * time.Second

	// waitDelay bounds how long Wait blocks on the child's output pipes once
	// the child itself has exited. A grandchild that escaped the process group
	// can still hold the write end open; without this, cleanup would hang
	// forever waiting for an EOF that never comes.
	waitDelay = 2 * time.Second

	// maxLogLine caps a single line of dev-server output. Vite can emit very
	// long lines (inlined source maps in stack traces) and the default
	// bufio.Scanner limit would abort the stream mid-run.
	maxLogLine = 1 << 20
)

// DevProcess is a running Vite development process, together with every
// process it spawned.
type DevProcess struct {
	command  *exec.Cmd
	exited   chan struct{} // closed once the child has been reaped
	waitErr  error         // valid only after exited is closed
	grace    time.Duration
	stopOnce sync.Once
	stopErr  error
}

// urlRegex matches Vite's standard local server URL in output (e.g. "http://localhost:5173" or "http://127.0.0.1:5174").
var urlRegex = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):\d+`)

// DevOrchestrator coordinates running Vite and the backend server concurrently.
// It handles OS signal interception (Ctrl+C / SIGINT / SIGTERM), termination of
// the whole dev-server process tree, Windows shell wrapping, log streaming, and
// automatically writes the .vite/hot marker file so no special Vite plugin is
// required.
//
// A framework lead engineer can embed or call this directly from their CLI dev command.
type DevOrchestrator struct {
	Command     string
	Dir         string
	HotFilePath string
	VitePort    int
	Stdout      io.Writer
	Stderr      io.Writer
	OnURL       func(string)
}

// viteOutputWriter forwards dev-server output to the terminal, rewriting the
// banner line that advertises Vite's own URL. Requests must go through the Go
// backend — clicking Vite's URL bypasses it and serves a page with no server
// state — so the address is replaced rather than printed.
type viteOutputWriter struct {
	target io.Writer
	buffer []byte
}

func (w *viteOutputWriter) Write(data []byte) (int, error) {
	w.buffer = append(w.buffer, data...)
	for {
		newline := bytes.IndexByte(w.buffer, '\n')
		if newline < 0 {
			break
		}
		line := w.buffer[:newline+1]
		w.buffer = w.buffer[newline+1:]
		if urlRegex.Match(line) && bytes.Contains(line, []byte("Local:")) {
			line = []byte("  ➜  Local: Vite development server running (internal)\n")
		}
		if _, err := w.target.Write(line); err != nil {
			return len(data), err
		}
	}
	return len(data), nil
}

func (w *viteOutputWriter) Flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	_, err := w.target.Write(w.buffer)
	w.buffer = nil
	return err
}

// NewDevOrchestrator returns a configured DevOrchestrator service ready to run.
func NewDevOrchestrator(command string, opts ...func(*DevOrchestrator)) *DevOrchestrator {
	if command == "" {
		command = "npm run dev"
	}
	orch := &DevOrchestrator{
		Command:     command,
		HotFilePath: ".vite/hot",
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}
	for _, opt := range opts {
		opt(orch)
	}
	return orch
}

// Run executes the Vite dev server and invokes the provided bootBackend function
// concurrently. It streams Vite's output to the terminal, detects the active
// server URL, automatically writes .vite/hot for the backend to read, catches
// termination signals (Ctrl+C / SIGINT / SIGTERM) via signal.NotifyContext, and
// guarantees cleanup of the whole dev-server process tree and the .vite/hot file.
func (o *DevOrchestrator) Run(parentCtx context.Context, bootBackend func(ctx context.Context) error) error {
	ctx, cancel := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	hotPath := o.HotFilePath
	if o.Dir != "" && !filepath.IsAbs(hotPath) {
		hotPath = filepath.Join(o.Dir, hotPath)
	}

	cleanupHotFile := func() {
		_ = os.Remove(hotPath)
	}
	// A previous interrupted run must not make the new backend connect to an old
	// Vite server before the current child process has started.
	cleanupHotFile()
	defer cleanupHotFile()

	// Checked before anything starts, because the shell's own diagnostic for a
	// missing program is "exit status 127", which says nothing about what is
	// missing or how to fix it — and it is the first thing a new user hits.
	if err := checkDevCommand(o.Command); err != nil {
		return err
	}

	vitePort := o.VitePort
	if vitePort == 0 {
		vitePort = findAvailableVitePort(0)
	}
	command := commandWithVitePort(o.Command, vitePort)

	var hotOnce sync.Once
	publishURL := func(rawURL string) {
		matches := urlRegex.FindString(rawURL)
		if matches == "" {
			return
		}
		hotOnce.Do(func() {
			u, err := url.Parse(matches)
			if err != nil {
				return
			}
			hostURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
			_ = os.MkdirAll(filepath.Dir(hotPath), 0755)
			// G703: hotPath is built from the orchestrator's own configured
			// directory and marker name, not from anything a request or a
			// remote party supplies.
			_ = os.WriteFile(hotPath, []byte(hostURL+"\n"), 0644) //nolint:gosec
			if o.OnURL != nil {
				o.OnURL(u.Host)
			}
		})
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				data, err := os.ReadFile(hotPath)
				if err == nil {
					publishURL(string(data))
				}
			}
		}
	}()

	// Vite's stdout and stderr are relayed to the caller's writers so build
	// errors, plugin failures and HMR messages actually reach the terminal.
	outReader, outWriter := io.Pipe()
	errReader, errorWriter := io.Pipe()
	var relays sync.WaitGroup
	relays.Add(2)
	go func() { defer relays.Done(); relayOutput(outReader, o.Stdout, publishURL) }()
	go func() { defer relays.Done(); relayOutput(errReader, o.Stderr, publishURL) }()

	closePipes := func() {
		_ = outWriter.Close()
		_ = errorWriter.Close()
	}

	proc, err := StartDevInDir(ctx, command, o.Dir, outWriter, errorWriter)
	if err != nil {
		closePipes()
		relays.Wait()
		return fmt.Errorf("vitekit: failed to start dev server: %w", err)
	}
	go func() {
		if waitForPort(ctx, vitePort) {
			publishURL(fmt.Sprintf("http://localhost:%d", vitePort))
		}
	}()
	defer func() {
		_ = proc.Stop()
		closePipes()
		relays.Wait()
	}()

	errCh := make(chan error, 2)

	// Monitor the Vite process tree.
	go func() {
		err := proc.Wait()
		closePipes()
		errCh <- err
	}()

	// Boot the core Go backend/router
	if bootBackend != nil {
		go func() {
			errCh <- bootBackend(ctx)
		}()
	}

	select {
	case <-ctx.Done():
		// Interrupted by signal
		_ = proc.Stop()
		cleanupHotFile()
		return ctx.Err()
	case err := <-errCh:
		// Either backend or Vite exited
		cancel()
		_ = proc.Stop()
		cleanupHotFile()
		return err
	}
}

// relayOutput forwards one of the dev server's output streams to the terminal,
// scanning each line for the server URL as it passes.
func relayOutput(reader io.Reader, target io.Writer, onLine func(string)) {
	writer := &viteOutputWriter{target: target}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLine)
	for scanner.Scan() {
		line := scanner.Text()
		onLine(line)
		if target != nil {
			_, _ = writer.Write([]byte(line + "\n"))
		}
	}
	if scanner.Err() != nil {
		_, _ = writer.Write(fmt.Append([]byte(scanner.Err().Error())))
	}
	if target != nil {
		_ = writer.Flush()
	}
}

// shellMetacharacters are the characters that mean the first word of a command
// is not simply a program name — a variable assignment, a subshell, a pipeline.
const shellMetacharacters = `"'$(){}[]<>|&;` + "`" + `\=`

// checkDevCommand verifies that the program a dev command starts is on PATH.
func checkDevCommand(command string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil
	}
	program := fields[0]
	if strings.ContainsAny(program, shellMetacharacters) {
		return nil // shell syntax; let the shell resolve it
	}
	if _, err := exec.LookPath(program); err != nil {
		return fmt.Errorf("vitekit: cannot start the dev server: %q is not installed or not on PATH.%s", program, devCommandHint(program))
	}
	return nil
}

// devCommandHint adds the advice that actually resolves the situation for the
// package managers people run Vite with.
func devCommandHint(program string) string {
	switch program {
	case "npm", "npx", "pnpm", "pnpx", "yarn", "bun", "bunx", "node", "deno":
		return "\n  Install Node.js (https://nodejs.org) and make sure " + program +
			" is on PATH, or pass --cmd with the command that starts Vite."
	default:
		return "\n  Pass --cmd with the command that starts your Vite dev server."
	}
}

func findAvailableVitePort(preferred int) int {
	if preferred < 1 {
		preferred = 5173
	}
	for port := preferred; port < preferred+100; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = listener.Close()
			return port
		}
	}
	return 5173
}

// runnerSeparators maps a dev-command prefix to the separator needed before
// flags that are meant for Vite rather than for the package manager itself.
// Script runners ("npm run dev") swallow trailing flags unless they follow
// "--"; direct invocations ("vite", "pnpm dev") pass them straight through.
var runnerSeparators = []struct {
	prefix    string
	separator string
}{
	{"npm run ", " -- "},
	{"npm exec ", " -- "},
	{"bun run ", " -- "},
	{"yarn run ", " -- "},
	{"pnpm run ", " -- "},
	{"yarn ", " "},
	{"pnpm ", " "},
	{"npx ", " "},
	{"bunx ", " "},
	{"deno task ", " "},
	{"vite", " "},
	{"./node_modules/.bin/vite", " "},
}

// commandWithVitePort appends host and port flags to the dev command so the
// backend knows where Vite will listen. A command that already pins a port is
// left untouched — the operator's choice wins, and the hot file still reports
// whatever Vite actually bound.
func commandWithVitePort(command string, port int) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || hasPortFlag(trimmed) {
		return command
	}
	flags := fmt.Sprintf("--host 127.0.0.1 --port %d", port)
	for _, runner := range runnerSeparators {
		if trimmed == strings.TrimSpace(runner.prefix) || strings.HasPrefix(trimmed, runner.prefix) {
			return trimmed + runner.separator + flags
		}
	}
	return command
}

func hasPortFlag(command string) bool {
	for _, field := range strings.Fields(command) {
		if field == "--port" || field == "-p" || strings.HasPrefix(field, "--port=") {
			return true
		}
	}
	return false
}

func waitForPort(ctx context.Context, port int) bool {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// StartDev starts the configured command without blocking the caller. The
// process tree is terminated when ctx is canceled.
func StartDev(ctx context.Context, command string, stdout, stderr io.Writer) (*DevProcess, error) {
	return StartDevInDir(ctx, command, "", stdout, stderr)
}

// StartDevInDir starts the configured command in a specific working directory.
func StartDevInDir(ctx context.Context, command string, dir string, stdout, stderr io.Writer) (*DevProcess, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Without WaitDelay, a process that outlived the group signal and still
	// holds the inherited output pipes would block Wait indefinitely.
	cmd.WaitDelay = waitDelay
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	process := &DevProcess{
		command: cmd,
		exited:  make(chan struct{}),
		grace:   StopGracePeriod,
	}
	go func() {
		process.waitErr = cmd.Wait()
		close(process.exited)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = process.Stop()
		case <-process.exited:
		}
	}()
	return process, nil
}

// Stop terminates the dev server and every process it spawned. It asks the
// process tree to exit first so Vite can release its port, then kills it
// outright if it is still alive after StopGracePeriod. Safe to call more than
// once and from several goroutines.
func (p *DevProcess) Stop() error {
	p.stopOnce.Do(func() {
		p.stopErr = p.terminate()
	})
	return p.stopErr
}

func (p *DevProcess) terminate() error {
	process := p.command.Process
	if process == nil {
		return nil
	}
	select {
	case <-p.exited:
		return nil // exited on its own; nothing to signal
	default:
	}

	if err := interruptProcessTree(process); err != nil {
		return p.forceKill(process)
	}
	select {
	case <-p.exited:
		return nil
	case <-time.After(p.grace):
		return p.forceKill(process)
	}
}

// forceKill terminates the tree and waits for it to actually be gone.
//
// Asking is not the same as the tree having exited: taskkill returns as soon as
// it has requested the kill, and until the processes really disappear Windows
// keeps their working directory and open files locked. A caller that deletes
// that directory next — a test using t.TempDir, a build script cleaning up —
// fails with a sharing violation. Waiting here is what makes Stop mean that the
// tree is down rather than merely doomed.
func (p *DevProcess) forceKill(process *os.Process) error {
	err := killProcessTree(process)
	select {
	case <-p.exited:
	case <-time.After(killConfirmPeriod):
	}
	return err
}

// Wait returns the process exit result. It may be called more than once and
// from several goroutines; every caller observes the same result.
func (p *DevProcess) Wait() error {
	<-p.exited
	return p.waitErr
}
