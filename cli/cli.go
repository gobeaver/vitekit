// Package cli implements vitekit's commands — dev, build and scaffold — as
// ordinary Go functions, so a host CLI can drive them instead of shelling out
// to the standalone vitekit binary.
//
// Nothing here parses flags, reads os.Args, writes to a stream it was not
// handed, or calls os.Exit. Every command returns an error, takes a
// context.Context the caller controls, and reports progress through an OnEvent
// callback so the host renders milestones in its own house style:
//
//	err := cli.Dev(ctx, cli.DevOptions{
//		Dir:     "./frontend",
//		Backend: "go run ./cmd/server",
//		Stdout:  cmd.OutOrStdout(),
//		Stderr:  cmd.ErrOrStderr(),
//		OnEvent: func(e cli.Event) { log.Info(e.Message) },
//	})
//
// Every option has a working default, so the zero DevOptions runs Vite in the
// current directory against the built-in preview server.
//
// The commands hold no package-level state. In particular [Dev]'s preview
// server builds its own [github.com/michael-amedaz/vitekit-gobeaver.Engine] rather than the
// process-wide one that vitekit.Init installs, so embedding these commands in a
// larger program cannot disturb an engine that program has already set up.
//
// cmd/vitekit is a thin shell over exactly these functions and is the reference for
// wiring them into a command of your own.
package cli

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// envPrefix is the environment-variable prefix the commands read vitekit
// settings under. It matches the default the root package uses.
const envPrefix = "VITEKIT_"

// EventKind identifies what a progress [Event] reports. Hosts that only want to
// print something can ignore it and use Event.Message.
type EventKind int

const (
	// EventStarting reports the resolved configuration of a dev run, once,
	// before anything is launched. Event.Plan is set.
	EventStarting EventKind = iota
	// EventBackendStarting reports that the backend command is about to run.
	EventBackendStarting
	// EventViteReady reports the address the Vite dev server came up on.
	// Event.URL is set.
	EventViteReady
	// EventPreviewReady reports the address of the built-in preview server,
	// which runs only when no backend command was given. Event.URL is set.
	EventPreviewReady
	// EventShutdown reports that Vite and the backend both stopped cleanly.
	EventShutdown
	// EventBuildStarted reports that the production build is about to run.
	EventBuildStarted
	// EventBuildComplete reports that the production build finished.
	EventBuildComplete
	// EventFileCreated reports a file written by Scaffold. Event.Path is set,
	// relative to the scaffolded directory.
	EventFileCreated
	// EventFileSkipped reports a file Scaffold left alone because it already
	// existed. Event.Path is set, relative to the scaffolded directory.
	EventFileSkipped
)

func (k EventKind) String() string {
	switch k {
	case EventStarting:
		return "starting"
	case EventBackendStarting:
		return "backend-starting"
	case EventViteReady:
		return "vite-ready"
	case EventPreviewReady:
		return "preview-ready"
	case EventShutdown:
		return "shutdown"
	case EventBuildStarted:
		return "build-started"
	case EventBuildComplete:
		return "build-complete"
	case EventFileCreated:
		return "file-created"
	case EventFileSkipped:
		return "file-skipped"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Plan is the fully resolved configuration of a dev run — defaults applied and
// the entry point detected — reported once through [EventStarting] so the host
// can print a header before the servers come up.
type Plan struct {
	Dir         string
	ViteCommand string
	Entry       string
	Backend     string // empty when the built-in preview server is used
	PreviewPort int    // 0 unless the built-in preview server is used
	PreviewHost string // interface the preview server binds; empty unless it is used
}

// Event is a progress report. Message is always set and is safe to print on its
// own; the remaining fields carry the same information in a form the host can
// act on, and are populated only for the kinds documented above.
type Event struct {
	Kind    EventKind
	Message string
	URL     string
	Path    string
	Plan    *Plan
}

// reporter adapts an optional OnEvent callback, so callers need not nil-check.
type reporter func(Event)

func (r reporter) emit(event Event) {
	if r != nil {
		r(event)
	}
}

// DetectEntry returns the first conventional Vite entry file present under dir,
// or "" if none is. It is what [Dev] falls back to when DevOptions.Entry is
// empty, exported so a host can resolve the entry itself — to show it in a
// prompt, or to fail early with its own diagnostic.
func DetectEntry(dir string) string {
	candidates := []string{
		"src/main.jsx", "src/main.tsx", "src/main.ts", "src/main.js",
		"src/index.jsx", "src/index.tsx", "src/index.ts", "src/index.js",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(dir, candidate)); err == nil {
			return candidate
		}
	}
	return ""
}

// availablePort returns the first free port at or after preferred, giving up
// and returning preferred after a hundred tries so a busy machine still gets a
// definite answer to bind against and report.
func availablePort(preferred int) int {
	for candidate := preferred; candidate < preferred+100; candidate++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", candidate))
		if err == nil {
			_ = listener.Close()
			return candidate
		}
	}
	return preferred
}

// writerOr keeps an unset writer from defaulting to the process's own streams:
// a library that writes to os.Stdout uninvited corrupts a host CLI's output.
func writerOr(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

func stringOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func intOr(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
