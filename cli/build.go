package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/michael-amedaz/vitekit-gobeaver"
)

// BuildOptions configures [Build]. Every field has a working default.
type BuildOptions struct {
	// Dir is the frontend directory containing package.json. Defaults to ".".
	Dir string
	// Command runs the production build. Defaults to "npm run build".
	Command string
	// Stdout and Stderr receive the build's output. A nil writer discards that
	// stream rather than falling back to the process's own.
	Stdout io.Writer
	Stderr io.Writer
	// OnEvent receives progress milestones. It may be nil.
	OnEvent func(Event)
}

// Build runs the production Vite build and blocks until it finishes, returning
// an error if the build command fails. Cancelling ctx terminates the build's
// whole process tree.
func Build(ctx context.Context, opts BuildOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	dir := stringOr(opts.Dir, ".")
	command := stringOr(opts.Command, "npm run build")
	report := reporter(opts.OnEvent)

	report.emit(Event{
		Kind:    EventBuildStarted,
		Message: fmt.Sprintf("building %s (%s)", dir, command),
	})
	process, err := vitekit.StartDevInDir(ctx, command, dir, writerOr(opts.Stdout), writerOr(opts.Stderr))
	if err != nil {
		return err
	}
	// Without this a cancelled context would leave the build's process tree
	// running after Build has returned.
	defer func() { _ = process.Stop() }()

	if err := process.Wait(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	report.emit(Event{Kind: EventBuildComplete, Message: "build complete"})
	return nil
}
