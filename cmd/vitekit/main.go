// Command vitekit runs a Vite frontend and a Go backend as one development
// process, and scaffolds the configuration needed to wire the two together.
//
// It is a thin shell: flag parsing, terminal formatting and exit codes only.
// Every command's behavior lives in github.com/michael-amedaz/vitekit-gobeaver/cli, so a
// host CLI can offer the same commands by calling that package directly. This
// file is the reference for how to do that.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/michael-amedaz/vitekit-gobeaver/cli"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "dev":
		os.Exit(runDev(os.Args[2:]))
	case "build":
		os.Exit(runBuild(os.Args[2:]))
	case "init":
		os.Exit(runInit(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Println("vitekit " + version())
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`vitekit — run a Vite frontend and a Go backend as one process.

Usage:
  vitekit dev [options]      Start Vite and your Go server together
  vitekit build [options]    Run the production Vite build
  vitekit init [options]     Scaffold Vite config for a Go backend
  vitekit version            Print the version

Run "vitekit <command> --help" for the options of a command.`)
}

func version() string {
	// Populated by the toolchain when installed with "go install".
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// fail prints an error the way this binary does and returns its exit code.
// The "vitekit:" check avoids "vitekit: vitekit: ..." on errors the package has
// already labelled.
func fail(err error) int {
	message := err.Error()
	if !strings.HasPrefix(message, "vitekit:") {
		message = "vitekit: " + message
	}
	fmt.Fprintf(os.Stderr, "%s\n", message)
	return 1
}

// --- dev ---

func runDev(args []string) int {
	flags := flag.NewFlagSet("dev", flag.ExitOnError)
	dir := flags.String("dir", ".", "Frontend directory containing package.json")
	command := flags.String("cmd", "npm run dev", "Command that starts the Vite dev server")
	backend := flags.String("backend", "", "Command that starts your Go server (default: a built-in preview server)")
	backendDir := flags.String("backend-dir", ".", "Working directory for the backend command")
	entry := flags.String("entry", "", "Vite entry source path (default: auto-detected)")
	vitePort := flags.Int("vite-port", 0, "Preferred Vite port (default: 5173, then the next free port)")
	port := flags.Int("port", 8080, "Port for the built-in preview server")
	previewHost := flags.String("preview-host", "127.0.0.1", "Interface the built-in preview server binds; use 0.0.0.0 to expose it")
	flags.Usage = func() {
		fmt.Println("Start Vite and your Go server together, with one Ctrl+C for both.")
		fmt.Println("\nOptions:")
		flags.PrintDefaults()
		fmt.Println(`
Examples:
  vitekit dev --dir ./frontend --backend "go run ./cmd/server"
  vitekit dev --dir ./frontend            # preview without a Go server yet`)
	}
	_ = flags.Parse(args)

	// Resolved here rather than left to cli.Dev so the diagnostic names the
	// flag a terminal user can actually set, and prints before the header.
	resolvedEntry := *entry
	if resolvedEntry == "" {
		resolvedEntry = cli.DetectEntry(*dir)
		if resolvedEntry == "" {
			return fail(fmt.Errorf("no entry file found under %s; pass --entry", *dir))
		}
	}

	err := cli.Dev(context.Background(), cli.DevOptions{
		Dir:         *dir,
		Command:     *command,
		Backend:     *backend,
		BackendDir:  *backendDir,
		Entry:       resolvedEntry,
		VitePort:    *vitePort,
		PreviewPort: *port,
		PreviewHost: *previewHost,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		OnEvent:     printDevEvent,
	})
	if err != nil {
		// The farewell belongs to an actual shutdown; printing it after a
		// failure to start says two servers stopped that never ran.
		fmt.Fprintln(os.Stderr)
		return fail(err)
	}
	return 0
}

func printDevEvent(event cli.Event) {
	switch event.Kind {
	case cli.EventStarting:
		fmt.Println("⚡ vitekit dev")
		fmt.Printf("   frontend   %s\n", event.Plan.Dir)
		fmt.Printf("   vite       %s\n", event.Plan.ViteCommand)
		fmt.Printf("   entry      %s\n", event.Plan.Entry)
		if event.Plan.Backend == "" {
			fmt.Printf("   backend    built-in preview server on %s:%d\n", event.Plan.PreviewHost, event.Plan.PreviewPort)
		} else {
			fmt.Printf("   backend    %s\n", event.Plan.Backend)
		}
		fmt.Println()
	case cli.EventViteReady:
		fmt.Printf("   vite ready on %s\n", event.URL)
	case cli.EventPreviewReady:
		fmt.Printf("   preview    %s\n\n", event.URL)
	case cli.EventShutdown:
		fmt.Printf("\n👋 %s\n", event.Message)
	}
}

// --- build ---

func runBuild(args []string) int {
	flags := flag.NewFlagSet("build", flag.ExitOnError)
	dir := flags.String("dir", ".", "Frontend directory containing package.json")
	command := flags.String("cmd", "npm run build", "Command that runs the production build")
	_ = flags.Parse(args)

	err := cli.Build(context.Background(), cli.BuildOptions{
		Dir:     *dir,
		Command: *command,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		OnEvent: func(event cli.Event) {
			switch event.Kind {
			case cli.EventBuildStarted:
				fmt.Printf("⚡ %s\n", event.Message)
			case cli.EventBuildComplete:
				fmt.Printf("✅ %s\n", event.Message)
			}
		},
	})
	if err != nil {
		return fail(err)
	}
	return 0
}

// --- init ---

func runInit(args []string) int {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	dir := flags.String("dir", ".", "Frontend directory to scaffold")
	entry := flags.String("entry", "src/main.js", "Vite entry source path to create and configure")
	_ = flags.Parse(args)

	result, err := cli.Scaffold(cli.ScaffoldOptions{
		Dir:   *dir,
		Entry: *entry,
		OnEvent: func(event cli.Event) {
			switch event.Kind {
			case cli.EventFileCreated:
				fmt.Printf("   created  %s\n", event.Path)
			case cli.EventFileSkipped:
				fmt.Printf("   skipped  %s (already exists)\n", event.Path)
			}
		},
	})
	if err != nil {
		return fail(err)
	}

	fmt.Printf("\nNext:\n\n%s", cli.NextSteps(result))
	return 0
}
