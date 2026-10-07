package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// longRunningCommand is a process that stays up until it is torn down, standing
// in for the Vite dev server. Matches the idiom the core package's tests use.
func longRunningCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 60 127.0.0.1"
	}
	return "sleep 60"
}

// devModeFixture is a scaffolded frontend with the hot-reload marker already in
// place, so an engine built against it comes up in development mode without a
// real Vite server behind it.
func devModeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vite", "hot"), []byte("http://localhost:5173\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectEntryPrefersTheFirstConventionalName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// index.js sorts after every main.* candidate, so main.ts must win even
	// though both exist.
	for _, name := range []string{"src/index.js", "src/main.ts"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("//"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := DetectEntry(dir); got != "src/main.ts" {
		t.Fatalf("DetectEntry = %q, want src/main.ts", got)
	}
}

func TestDetectEntryReportsNothingWhenNoEntryExists(t *testing.T) {
	if got := DetectEntry(t.TempDir()); got != "" {
		t.Fatalf("DetectEntry = %q, want empty", got)
	}
}

func TestScaffoldWritesTheWholeSetup(t *testing.T) {
	dir := t.TempDir()

	var events []Event
	result, err := Scaffold(ScaffoldOptions{
		Dir:     dir,
		OnEvent: func(event Event) { events = append(events, event) },
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"vite.config.js", "package.json", "src/main.js"}
	if strings.Join(result.Created, ",") != strings.Join(want, ",") {
		t.Fatalf("created %v, want %v", result.Created, want)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("skipped %v on a fresh directory, want none", result.Skipped)
	}
	if result.Entry != "src/main.js" {
		t.Fatalf("result entry = %q, want the default src/main.js", result.Entry)
	}
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}

	// One event per file, and every event must be printable on its own.
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}
	for _, event := range events {
		if event.Kind != EventFileCreated {
			t.Errorf("event kind = %s, want file-created", event.Kind)
		}
		if event.Message == "" || event.Path == "" {
			t.Errorf("event has empty Message or Path: %+v", event)
		}
	}

	// The generated config must name the entry, or Vite builds the wrong input.
	config, err := os.ReadFile(filepath.Join(dir, "vite.config.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(config, []byte("'src/main.js'")) {
		t.Errorf("vite.config.js does not reference the entry:\n%s", config)
	}
}

func TestScaffoldHonorsACustomEntry(t *testing.T) {
	dir := t.TempDir()
	result, err := Scaffold(ScaffoldOptions{Dir: dir, Entry: "app/boot.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "boot.ts")); err != nil {
		t.Fatalf("nested entry not created: %v", err)
	}
	if !strings.Contains(NextSteps(result), "app/boot.ts") {
		t.Error("NextSteps does not mention the custom entry")
	}
}

// Re-running init over a real project must not destroy work in progress.
func TestScaffoldNeverClobbersExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	mine := []byte("// hand-written\n")
	if err := os.WriteFile(filepath.Join(dir, "src/main.js"), mine, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scaffold(ScaffoldOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 {
		t.Errorf("created %v on a second run, want none", result.Created)
	}
	if len(result.Skipped) != 3 {
		t.Errorf("skipped %v, want all three files", result.Skipped)
	}
	after, err := os.ReadFile(filepath.Join(dir, "src/main.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, mine) {
		t.Errorf("entry file was overwritten:\n%s", after)
	}
}

// The go:embed directive rejects a leading slash and any "." or ".." element,
// so the wiring NextSteps prints has to be a path it will actually accept.
func TestNextStepsEmitsAUsableEmbedDirective(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"relative with dot slash", "./frontend", "//go:embed all:frontend/dist"},
		{"bare relative", "frontend", "//go:embed all:frontend/dist"},
		{"nested relative", "web/frontend", "//go:embed all:web/frontend/dist"},
		{"trailing slash", "./frontend/", "//go:embed all:frontend/dist"},
		// The frontend is the Go source directory itself, so "./dist" — which
		// go:embed rejects for its "." element — must come out as "dist".
		{"current directory", ".", "//go:embed all:dist"},
		// An absolute path is re-expressed against the working directory.
		{"absolute inside the tree", filepath.Join(workingDir, "frontend"), "//go:embed all:frontend/dist"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := NextSteps(ScaffoldResult{Dir: testCase.dir, Entry: "src/main.js"})
			if !strings.Contains(got, testCase.want) {
				t.Errorf("NextSteps(%q) missing %q:\n%s", testCase.dir, testCase.want, got)
			}
			for _, bad := range []string{"all:/", "all:./", "all:../", "//dist"} {
				if strings.Contains(got, bad) {
					t.Errorf("NextSteps(%q) emitted an invalid pattern containing %q:\n%s", testCase.dir, bad, got)
				}
			}
		})
	}
}

// A frontend go:embed cannot reach needs different advice, not a directive that
// fails to compile.
func TestNextStepsFallsBackWhenTheFrontendCannotBeEmbedded(t *testing.T) {
	outside := t.TempDir()
	if !filepath.IsAbs(outside) {
		t.Skipf("TempDir is not absolute: %s", outside)
	}

	got := NextSteps(ScaffoldResult{Dir: outside, Entry: "src/main.js"})
	// The prose may still name go:embed to explain itself; what must not appear
	// is a directive someone could paste.
	if strings.Contains(got, "//go:embed") {
		t.Errorf("suggested a go:embed directive for an unreachable directory:\n%s", got)
	}
	if !strings.Contains(got, "os.DirFS(") {
		t.Errorf("no os.DirFS fallback offered:\n%s", got)
	}
	// The shell commands still want the real path; only the Go wiring changes.
	if !strings.Contains(got, "npm --prefix "+outside) {
		t.Errorf("install command lost the directory:\n%s", got)
	}
}

// Dev cannot guess an entry, and failing here beats starting Vite and serving a
// page that renders nothing.
func TestDevFailsWhenNoEntryCanBeDetected(t *testing.T) {
	err := Dev(context.Background(), DevOptions{Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when no entry exists")
	}
	if !strings.Contains(err.Error(), "no entry file found") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestBuildReportsCommandFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	var completed bool
	err := Build(ctx, BuildOptions{
		Dir:     t.TempDir(),
		Command: "exit 3",
		Stdout:  &out,
		OnEvent: func(event Event) {
			if event.Kind == EventBuildComplete {
				completed = true
			}
		},
	})
	if err == nil {
		t.Fatal("expected an error from a failing build command")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("unlabelled error: %v", err)
	}
	if completed {
		t.Error("reported build-complete for a failed build")
	}
}

func TestBuildSucceedsAndReportsProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	var kinds []EventKind
	err := Build(ctx, BuildOptions{
		Dir:     t.TempDir(),
		Command: "echo built",
		Stdout:  &out,
		OnEvent: func(event Event) { kinds = append(kinds, event.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "built" {
		t.Errorf("command output = %q, want %q", got, "built")
	}
	if len(kinds) != 2 || kinds[0] != EventBuildStarted || kinds[1] != EventBuildComplete {
		t.Errorf("event kinds = %v, want [build-started build-complete]", kinds)
	}
}

// A library that writes to os.Stdout uninvited corrupts a host CLI's output, so
// unset writers must discard rather than fall back to the process streams.
func TestUnsetWritersDiscardRatherThanReachingForStdout(t *testing.T) {
	processStdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	defer func() { os.Stdout = processStdout }()

	buildErr := Build(context.Background(), BuildOptions{Dir: t.TempDir(), Command: "echo leaked"})
	os.Stdout = processStdout
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	if buildErr != nil {
		t.Fatal(buildErr)
	}

	var captured bytes.Buffer
	if _, err := captured.ReadFrom(read); err != nil {
		t.Fatal(err)
	}
	if captured.Len() != 0 {
		t.Errorf("build wrote to os.Stdout with no writer set: %q", captured.String())
	}
}

// A nil OnEvent is the common case for a host that only wants the error.
func TestCommandsTolerateANilEventCallback(t *testing.T) {
	if _, err := Scaffold(ScaffoldOptions{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), BuildOptions{Dir: t.TempDir(), Command: "true"}); err != nil {
		t.Fatal(err)
	}
}

// A development server on every interface is reachable by anyone sharing the
// network, so an unset host must mean loopback rather than 0.0.0.0.
func TestListenAddressKeepsThePreviewOffTheNetwork(t *testing.T) {
	if got := listenAddress("", 8080); got != "127.0.0.1:8080" {
		t.Errorf("listenAddress(\"\", 8080) = %q, want 127.0.0.1:8080", got)
	}
	// An explicit choice is still honored, for containers and other devices.
	if got := listenAddress("0.0.0.0", 3000); got != "0.0.0.0:3000" {
		t.Errorf("listenAddress(\"0.0.0.0\", 3000) = %q, want 0.0.0.0:3000", got)
	}
	if got := listenAddress("::1", 3000); got != "[::1]:3000" {
		t.Errorf("listenAddress(\"::1\", 3000) = %q, want [::1]:3000", got)
	}
}

// The preview server is what someone sees before they have written a backend,
// so it has to render the entry against the running dev server.
func TestPreviewServerRendersTheEntry(t *testing.T) {
	dir := devModeFixture(t)
	port := availablePort(8400)

	var ready string
	run := previewRunner(dir, "src/main.js", "127.0.0.1", port, func(event Event) {
		if event.Kind == EventPreviewReady {
			ready = event.URL
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()

	address := "http://127.0.0.1:" + strconvItoa(port)
	body, status := getWithRetry(t, address+"/")
	if status != http.StatusOK {
		t.Fatalf("preview page returned %d:\n%s", status, body)
	}
	// Development mode: the tags must point at the dev server, not at a build.
	if !strings.Contains(body, "http://localhost:5173/src/main.js") {
		t.Errorf("entry not wired to the dev server:\n%s", body)
	}
	if !strings.Contains(body, `id="app"`) {
		t.Errorf("mount point missing:\n%s", body)
	}
	if ready == "" {
		t.Error("no EventPreviewReady was reported")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("preview server returned %v on shutdown, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("preview server did not shut down")
	}
}

// The preview server must answer for the entry only; anything else is a 404
// rather than a directory listing or a stack trace.
func TestPreviewServerServesAssetsAndNotTheTree(t *testing.T) {
	dir := devModeFixture(t)
	port := availablePort(8500)
	run := previewRunner(dir, "src/main.js", "127.0.0.1", port, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = run(ctx) }()

	address := "http://127.0.0.1:" + strconvItoa(port)
	if _, status := getWithRetry(t, address+"/"); status != http.StatusOK {
		t.Fatalf("preview page returned %d", status)
	}
	// vite.config.js sits in the frontend directory but is not build output.
	if _, status := getWithRetry(t, address+"/assets/vite.config.js"); status == http.StatusOK {
		t.Error("served a source file out of the frontend directory")
	}
}

// A backend that exits cleanly ends the dev session cleanly: Dev returns nil and
// reports the shutdown rather than treating the exit as a failure.
func TestDevRunsTheBackendAndStopsWithIt(t *testing.T) {
	dir := devModeFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var kinds []EventKind
	var output bytes.Buffer
	err := Dev(ctx, DevOptions{
		Dir:     dir,
		Entry:   "src/main.js",
		Command: longRunningCommand(),
		Backend: "echo backend-ran",
		Stdout:  &output,
		Stderr:  io.Discard,
		OnEvent: func(event Event) { kinds = append(kinds, event.Kind) },
	})
	if err != nil {
		t.Fatalf("Dev returned %v, want nil", err)
	}
	if !strings.Contains(output.String(), "backend-ran") {
		t.Errorf("backend output not relayed to Stdout: %q", output.String())
	}
	if !containsKind(kinds, EventStarting) || !containsKind(kinds, EventBackendStarting) || !containsKind(kinds, EventShutdown) {
		t.Errorf("event kinds = %v, want starting, backend-starting and shutdown", kinds)
	}
}

// A backend that fails must surface as an error, not a clean shutdown.
func TestDevSurfacesABackendFailure(t *testing.T) {
	dir := devModeFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var shutdown bool
	err := Dev(ctx, DevOptions{
		Dir:     dir,
		Entry:   "src/main.js",
		Command: longRunningCommand(),
		Backend: "exit 7",
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		OnEvent: func(event Event) {
			if event.Kind == EventShutdown {
				shutdown = true
			}
		},
	})
	if err == nil {
		t.Fatal("Dev returned nil for a backend that exited 7")
	}
	if shutdown {
		t.Error("reported a clean shutdown for a failed backend")
	}
}

// Cancelling the caller's context is an ordinary stop, not an error.
func TestDevTreatsCancellationAsACleanStop(t *testing.T) {
	dir := devModeFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		done <- Dev(ctx, DevOptions{
			Dir:     dir,
			Entry:   "src/main.js",
			Command: longRunningCommand(),
			Backend: longRunningCommand(),
			Stdout:  io.Discard,
			Stderr:  io.Discard,
			OnEvent: func(event Event) {
				if event.Kind == EventBackendStarting {
					close(started)
				}
			},
		})
	}()

	select {
	case <-started:
	case err := <-done:
		t.Fatalf("Dev returned before the backend started: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("backend never started")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Dev returned %v after cancellation, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Dev did not return after cancellation")
	}
}

func containsKind(kinds []EventKind, want EventKind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func strconvItoa(value int) string {
	return fmt.Sprintf("%d", value)
}

// getWithRetry gives the server a moment to bind before the first request.
func getWithRetry(t *testing.T, url string) (string, int) {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 100; attempt++ {
		response, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		defer func() { _ = response.Body.Close() }()
		body, _ := io.ReadAll(response.Body)
		return string(body), response.StatusCode
	}
	t.Fatalf("no response from %s: %v", url, lastErr)
	return "", 0
}

// Event kinds appear in host logs and test failures, so the names have to be
// real rather than "EventKind(3)".
func TestEventKindNames(t *testing.T) {
	for kind, want := range map[EventKind]string{
		EventStarting:        "starting",
		EventBackendStarting: "backend-starting",
		EventViteReady:       "vite-ready",
		EventPreviewReady:    "preview-ready",
		EventShutdown:        "shutdown",
		EventBuildStarted:    "build-started",
		EventBuildComplete:   "build-complete",
		EventFileCreated:     "file-created",
		EventFileSkipped:     "file-skipped",
	} {
		if got := kind.String(); got != want {
			t.Errorf("EventKind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
	if got := EventKind(99).String(); !strings.Contains(got, "99") {
		t.Errorf("unknown kind rendered as %q, want it to name the number", got)
	}
}

// With no backend command Dev stands up the preview server itself, choosing the
// port and the loopback host. This is the path someone hits first.
//
// The orchestrator clears any stale .vite/hot before it starts, so a marker
// seeded by the fixture is deliberately gone by the time the preview serves a
// request. Development mode has to be reached the way it is in real use: the
// dev server's port opens, the orchestrator notices and writes the marker
// itself. A bare listener on the port it was told to use is enough to trigger
// that without a Node toolchain.
func TestDevStartsThePreviewWhenThereIsNoBackend(t *testing.T) {
	dir := devModeFixture(t)

	vitePort := availablePort(5600)
	stubVite, err := net.Listen("tcp", listenAddress("127.0.0.1", vitePort))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stubVite.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan *Plan, 1)
	var previewURL string

	go func() {
		done <- Dev(ctx, DevOptions{
			Dir:      dir,
			Entry:    "src/main.js",
			Command:  longRunningCommand(),
			VitePort: vitePort,
			Stdout:   io.Discard,
			Stderr:   io.Discard,
			OnEvent: func(event Event) {
				switch event.Kind {
				case EventStarting:
					ready <- event.Plan
				case EventPreviewReady:
					previewURL = event.URL
				}
			},
		})
	}()

	var plan *Plan
	select {
	case plan = <-ready:
	case err := <-done:
		t.Fatalf("Dev returned before starting: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("Dev never reported EventStarting")
	}

	if plan.Backend != "" {
		t.Errorf("plan names a backend %q, want none", plan.Backend)
	}
	if plan.PreviewPort == 0 {
		t.Error("plan did not resolve a preview port")
	}
	if plan.PreviewHost != "127.0.0.1" {
		t.Errorf("preview host = %q, want the loopback default", plan.PreviewHost)
	}

	// The engine only leaves production mode once the orchestrator has seen the
	// dev server's port and written .vite/hot, so poll for that rather than
	// racing it.
	var body string
	var status int
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, status = getWithRetry(t, "http://127.0.0.1:"+strconvItoa(plan.PreviewPort)+"/")
		if status == http.StatusOK || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != http.StatusOK {
		t.Fatalf("preview returned %d:\n%s", status, body)
	}
	if want := "http://localhost:" + strconvItoa(vitePort) + "/src/main.js"; !strings.Contains(body, want) {
		t.Errorf("entry not wired to the dev server at %s:\n%s", want, body)
	}
	if previewURL == "" {
		t.Error("no EventPreviewReady was reported")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Dev returned %v after cancellation, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Dev did not return after cancellation")
	}
}
