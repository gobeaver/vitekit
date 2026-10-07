package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/michael-amedaz/vitekit-gobeaver"
)

// defaultPreviewPort is where the built-in preview server starts looking for a
// free port.
const defaultPreviewPort = 8080

// defaultPreviewHost keeps the preview server off the local network. Binding
// every interface would put an unauthenticated development server, and whatever
// the application renders into it, in reach of anyone sharing the office or
// cafe Wi-Fi.
const defaultPreviewHost = "127.0.0.1"

const (
	// previewReadHeaderTimeout bounds how long a client may take to send its
	// request headers.
	previewReadHeaderTimeout = 10 * time.Second
	// previewShutdownGrace bounds how long shutdown waits for in-flight
	// requests before giving up.
	previewShutdownGrace = 5 * time.Second
)

// DevOptions configures [Dev]. Every field has a working default.
type DevOptions struct {
	// Dir is the frontend directory containing package.json. Defaults to ".".
	Dir string
	// Command starts the Vite dev server. Defaults to "npm run dev".
	Command string
	// Backend starts the application's own server. When empty, Dev runs a
	// built-in preview server instead, so the wiring can be seen working
	// before any application code exists.
	Backend string
	// BackendDir is the working directory for Backend. Defaults to ".".
	BackendDir string
	// Entry is the Vite entry source path. When empty it is detected with
	// [DetectEntry], and Dev fails if nothing is found.
	Entry string
	// VitePort is the preferred port for the Vite dev server. Zero lets
	// vitekit pick (5173, then the next free port).
	VitePort int
	// PreviewPort is where the built-in preview server starts looking for a
	// free port. Zero means 8080. Ignored when Backend is set.
	PreviewPort int
	// PreviewHost is the interface the built-in preview server binds. Empty
	// means 127.0.0.1, which keeps a development server off the local network;
	// set it to "0.0.0.0" only when something outside the machine has to reach
	// it, as from a container or another device. Ignored when Backend is set.
	PreviewHost string
	// Stdout and Stderr receive the raw output of Vite and the backend. A nil
	// writer discards that stream rather than falling back to the process's
	// own, which would corrupt a host CLI's output.
	Stdout io.Writer
	Stderr io.Writer
	// OnEvent receives progress milestones. It may be nil.
	OnEvent func(Event)
}

// Dev runs the Vite dev server and the backend together and blocks until both
// stop. Cancelling ctx shuts them both down, as does an interrupt: Dev traps
// SIGINT and SIGTERM for the duration of the call and tears down the whole
// dev-server process tree.
//
// A shutdown from either source is success, so a host CLI reports an error only
// when one actually occurred.
func Dev(ctx context.Context, opts DevOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	dir := stringOr(opts.Dir, ".")
	command := stringOr(opts.Command, "npm run dev")
	stdout := writerOr(opts.Stdout)
	stderr := writerOr(opts.Stderr)
	report := reporter(opts.OnEvent)

	entry := opts.Entry
	if entry == "" {
		entry = DetectEntry(dir)
		if entry == "" {
			return fmt.Errorf("vitekit: no entry file found under %s; set DevOptions.Entry", dir)
		}
	}

	plan := Plan{Dir: dir, ViteCommand: command, Entry: entry, Backend: opts.Backend}
	var boot func(context.Context) error
	if opts.Backend == "" {
		plan.PreviewPort = availablePort(intOr(opts.PreviewPort, defaultPreviewPort))
		plan.PreviewHost = stringOr(opts.PreviewHost, defaultPreviewHost)
		boot = previewRunner(dir, entry, plan.PreviewHost, plan.PreviewPort, report)
	} else {
		boot = backendRunner(opts.Backend, stringOr(opts.BackendDir, "."), stdout, stderr, report)
	}
	report.emit(Event{Kind: EventStarting, Message: "vitekit dev", Plan: &plan})

	orchestrator := vitekit.NewDevOrchestrator(command, func(options *vitekit.DevOrchestrator) {
		options.Dir = dir
		options.VitePort = opts.VitePort
		options.Stdout = stdout
		options.Stderr = stderr
		options.OnURL = func(host string) {
			report.emit(Event{Kind: EventViteReady, Message: "vite ready on " + host, URL: host})
		}
	})

	// A cancelled context is how a clean Ctrl+C arrives here, not a failure.
	if err := orchestrator.Run(ctx, boot); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	report.emit(Event{Kind: EventShutdown, Message: "Vite and the backend were both stopped."})
	return nil
}

// backendRunner runs the caller's own server as a child process. Its output
// goes straight to the supplied writers, and the orchestrator tears down its
// whole process tree along with Vite's.
func backendRunner(command, dir string, stdout, stderr io.Writer, report reporter) func(context.Context) error {
	return func(ctx context.Context) error {
		report.emit(Event{Kind: EventBackendStarting, Message: "backend " + command})
		process, err := vitekit.StartDevInDir(ctx, command, dir, stdout, stderr)
		if err != nil {
			return fmt.Errorf("start backend: %w", err)
		}
		defer func() { _ = process.Stop() }()
		return process.Wait()
	}
}

// previewRunner is what runs when no backend command is given: enough of a Go
// server to prove the wiring works, so scaffolding a project and starting it
// shows a live page before any application code exists.
//
// It builds its own Engine rather than calling the package-level vitekit.Init,
// so that embedding Dev in a larger program cannot clobber an engine that
// program already installed.
func previewRunner(dir, entry, host string, port int, report reporter) func(context.Context) error {
	return func(ctx context.Context) error {
		engine, cancel, err := vitekit.WithPrefix(envPrefix).New(
			os.DirFS(dir),
			vitekit.WithDevMode(),
			vitekit.WithEntry(entry),
			vitekit.WithRootDir(dir),
		)
		if err != nil {
			return fmt.Errorf("start preview server: %w", err)
		}
		defer cancel()

		mux := http.NewServeMux()
		if assets, err := engine.AssetHandler(); err == nil {
			mux.Handle("/assets/", assets)
		}
		mux.HandleFunc("/", previewPage(engine, entry))

		server := &http.Server{
			Addr:    listenAddress(host, port),
			Handler: mux,
			// A client that opens a connection and dribbles headers would
			// otherwise hold it open indefinitely.
			ReadHeaderTimeout: previewReadHeaderTimeout,
		}
		//nolint:gosec // G118: ctx is already done by the time this runs, so
		// Shutdown needs a live context of its own; see the comment below.
		go func() {
			<-ctx.Done()
			// Deliberately not ctx: it is already done, and Shutdown needs a
			// live context to wait on in-flight requests. Bounded so a stuck
			// request cannot hold the whole dev session open.
			stopCtx, stopCancel := context.WithTimeout(context.Background(), previewShutdownGrace)
			defer stopCancel()
			_ = server.Shutdown(stopCtx)
		}()

		address := fmt.Sprintf("http://localhost:%d", port)
		report.emit(Event{Kind: EventPreviewReady, Message: "preview " + address, URL: address})
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// listenAddress builds the address the preview server binds. An empty host
// means the loopback default rather than every interface, so a caller that
// leaves PreviewHost unset cannot accidentally publish the server.
func listenAddress(host string, port int) string {
	return net.JoinHostPort(stringOr(host, defaultPreviewHost), strconv.Itoa(port))
}

func previewPage(engine *vitekit.Engine, entry string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		nonce, err := vitekit.NewNonce()
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		tags, err := engine.TagsWithOptions(entry, vitekit.RenderOptions{Nonce: nonce})
		if err != nil {
			http.Error(writer, fmt.Sprintf("vitekit: render entry: %v", err), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Security-Policy", engine.ContentSecurityPolicy(nonce))
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(writer, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>vitekit preview</title>
%s
%s
</head>
<body>
<div id="app"></div>
<div id="root"></div>
</body>
</html>`, engine.ReactPreamble(), tags)
	}
}
