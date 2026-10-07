package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScaffoldOptions configures [Scaffold]. Every field has a working default.
type ScaffoldOptions struct {
	// Dir is the frontend directory to scaffold. Defaults to ".".
	Dir string
	// Entry is the Vite entry source path to create and configure. Defaults to
	// "src/main.js".
	Entry string
	// OnEvent receives one EventFileCreated or EventFileSkipped per file. It
	// may be nil; the same information is in the returned ScaffoldResult.
	OnEvent func(Event)
}

// ScaffoldResult lists what [Scaffold] did, with paths relative to the
// scaffolded directory. Skipped names files that already existed.
type ScaffoldResult struct {
	Dir     string
	Entry   string
	Created []string
	Skipped []string
}

// Scaffold writes the Vite configuration a Go backend needs — vite.config.js,
// package.json and the entry file — into the target directory. A file that
// already exists is never clobbered; it is recorded in ScaffoldResult.Skipped
// instead, which makes Scaffold safe to run again over a partial setup.
func Scaffold(opts ScaffoldOptions) (ScaffoldResult, error) {
	dir := stringOr(opts.Dir, ".")
	entry := stringOr(opts.Entry, "src/main.js")
	report := reporter(opts.OnEvent)
	result := ScaffoldResult{Dir: dir, Entry: entry}

	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(entry)), 0o755); err != nil {
		return result, err
	}

	files := []struct {
		path    string
		content string
	}{
		{"vite.config.js", viteConfigTemplate(entry)},
		{"package.json", packageJSONTemplate()},
		{entry, entryTemplate()},
	}
	for _, file := range files {
		target := filepath.Join(dir, file.path)
		if _, err := os.Stat(target); err == nil {
			result.Skipped = append(result.Skipped, file.path)
			report.emit(Event{
				Kind:    EventFileSkipped,
				Message: fmt.Sprintf("skipped %s (already exists)", file.path),
				Path:    file.path,
			})
			continue
		}
		if err := os.WriteFile(target, []byte(file.content), 0o644); err != nil {
			return result, fmt.Errorf("writing %s: %w", file.path, err)
		}
		result.Created = append(result.Created, file.path)
		report.emit(Event{
			Kind:    EventFileCreated,
			Message: "created " + file.path,
			Path:    file.path,
		})
	}
	return result, nil
}

// NextSteps returns the instructions that follow a scaffold: installing
// dependencies, starting the dev server, and the Go side of the wiring. It is
// content rather than presentation, so a host CLI can print it verbatim under
// its own heading.
//
// The Go half depends on where the frontend landed: a directory go:embed can
// reach gets an embed directive, and one it cannot gets os.DirFS instead.
func NextSteps(result ScaffoldResult) string {
	wiring := fmt.Sprintf(`  // The frontend is outside the directory your Go source lives in, and a
  // go:embed pattern cannot reach past it. Move the frontend under your
  // module to embed it, or read it from disk:

  vitekit.Init(os.DirFS(%q), vitekit.WithEntry(%q))
`, result.Dir, result.Entry)

	if pattern, ok := embedPattern(result.Dir); ok {
		wiring = fmt.Sprintf(`  //go:embed all:%s
  var assets embed.FS

  vitekit.Init(assets, vitekit.WithEntry(%q))
`, pattern, result.Entry)
	}

	return fmt.Sprintf(`  npm --prefix %s install
  vitekit dev --dir %s --backend "go run ."

In your Go server:

%s`, result.Dir, result.Dir, wiring)
}

// embedPattern turns the scaffolded directory into a go:embed pattern, or
// reports that no valid one exists.
//
// The directive takes a slash-separated path relative to the source file, and
// rejects a leading slash as well as any "." or ".." element — so an absolute
// directory has to be re-expressed against the working directory the scaffold
// ran in, which is where the Go source is presumed to live. A directory that
// still points outside it cannot be embedded at all, and saying so beats
// printing a directive that does not compile.
func embedPattern(dir string) (string, bool) {
	if filepath.IsAbs(dir) {
		workingDir, err := os.Getwd()
		if err != nil {
			return "", false
		}
		relative, err := filepath.Rel(workingDir, dir)
		if err != nil {
			return "", false
		}
		dir = relative
	}

	dir = filepath.ToSlash(filepath.Clean(dir))
	switch {
	case dir == ".":
		// The frontend is the Go source directory itself.
		return "dist", true
	case dir == ".." || strings.HasPrefix(dir, "../"):
		return "", false
	}
	return dir + "/dist", true
}

func viteConfigTemplate(entry string) string {
	return fmt.Sprintf(`import { defineConfig } from 'vite'

export default defineConfig({
  build: {
    // vitekit reads .vite/manifest.json to resolve hashed filenames, CSS
    // chains and code-split chunks. Without this there is no manifest to read.
    manifest: true,
    outDir: 'dist',
    rollupOptions: {
      input: '%s',
    },
  },
  server: {
    // The Go server owns the HTML document and loads modules from Vite's
    // origin, so those requests are cross-origin and need CORS.
    cors: true,
  },
  // The Go orchestrator writes and removes .vite/hot itself, so no
  // hot-file plugin is needed here.
})
`, entry)
}

func packageJSONTemplate() string {
	return `{
  "name": "frontend",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build"
  },
  "devDependencies": {
    "vite": "^5.0.0"
  }
}
`
}

func entryTemplate() string {
	return `const app = document.querySelector('#app')
if (app) {
  app.textContent = 'Vite and Go are connected.'
}
`
}
