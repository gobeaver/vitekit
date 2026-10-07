package vitekit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func TestRefreshDevURLReadsHotFile(t *testing.T) {
	fsys := fstest.MapFS{
		".vite/hot": {Data: []byte("http://localhost:5173\n")},
	}
	cfg := defaultConfig()
	engine := newEngine(fsys, cfg)
	w := NewWatcher(cfg, fsys, engine)

	w.refreshDevURL()

	url, ok := engine.DevURL()
	if !ok {
		t.Fatal("expected dev URL to be set")
	}
	if url != "http://localhost:5173" {
		t.Fatalf("unexpected dev URL: %q", url)
	}
}

func TestRefreshDevURLClearsWhenMissing(t *testing.T) {
	fsys := fstest.MapFS{} // no .vite/hot
	cfg := defaultConfig()
	engine := newEngine(fsys, cfg)

	// Pre-set a dev URL.
	existing := "http://localhost:5173"
	engine.devURL.Store(&existing)

	w := NewWatcher(cfg, fsys, engine)
	w.refreshDevURL()

	if _, ok := engine.DevURL(); ok {
		t.Fatal("expected dev URL to be cleared when hot file is absent")
	}
}

func TestSamePathVariants(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{".vite/hot", ".vite/hot", true},
		{filepath.Join(".", ".vite", "hot"), ".vite/hot", true},
		{"a/b", "a/c", false},
	}
	for _, tt := range tests {
		if got := samePath(tt.a, tt.b); got != tt.want {
			t.Errorf("samePath(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestReloadManifestAtomicUpdate(t *testing.T) {
	manifest1 := []byte(`{"src/main.js":{"file":"assets/main.111.js"}}`)
	manifest2 := []byte(`{"src/main.js":{"file":"assets/main.222.js"}}`)

	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: manifest1},
	}
	cfg := defaultConfig()
	engine := newEngine(fsys, cfg)

	if err := engine.reloadManifest(); err != nil {
		t.Fatal(err)
	}
	entry, ok := engine.lookup("src/main.js")
	if !ok || entry.File != "assets/main.111.js" {
		t.Fatalf("unexpected entry before update: %v, %v", entry, ok)
	}

	// Update the file in place (simulating a build update during rolling deploys)
	fsys[".vite/manifest.json"] = &fstest.MapFile{Data: manifest2}
	if err := engine.reloadManifest(); err != nil {
		t.Fatal(err)
	}
	entry, ok = engine.lookup("src/main.js")
	if !ok || entry.File != "assets/main.222.js" {
		t.Fatalf("unexpected entry after update: %v, %v", entry, ok)
	}
}

// The engine reads through fs.FS but fsnotify needs real OS paths. When the
// frontend lives outside the working directory, resolving the watch against
// the CWD attaches it to the wrong directory entirely.
func TestWatcherResolvesOSPathsAgainstRootDir(t *testing.T) {
	config := defaultConfig()
	config.RootDir = filepath.Join("some", "frontend")
	watcher := NewWatcher(config, fstest.MapFS{}, newEngine(fstest.MapFS{}, config))

	wantHot := filepath.Join("some", "frontend", ".vite", "hot")
	if got := watcher.hotOSPath(); got != wantHot {
		t.Errorf("hotOSPath() = %q, want %q", got, wantHot)
	}
	wantManifest := filepath.Join("some", "frontend", ".vite", "manifest.json")
	if got := watcher.manifestOSPath(); got != wantManifest {
		t.Errorf("manifestOSPath() = %q, want %q", got, wantManifest)
	}
}

func TestWatcherOSPathKeepsAbsolutePaths(t *testing.T) {
	config := defaultConfig()
	config.RootDir = "frontend"
	absolute := filepath.Join(t.TempDir(), "hot")
	config.HotFilePath = absolute
	watcher := NewWatcher(config, fstest.MapFS{}, newEngine(fstest.MapFS{}, config))

	if got := watcher.hotOSPath(); got != absolute {
		t.Errorf("hotOSPath() = %q, want the absolute path %q", got, absolute)
	}
}

// End-to-end: a dev server starting in a directory that is not the working
// directory must still flip the engine into dev mode.
func TestStartWatchingDetectsHotFileOutsideWorkingDirectory(t *testing.T) {
	frontend := t.TempDir()
	config := defaultConfig()
	config.RootDir = frontend
	config.AllowMissingManifest = true

	engine := newEngine(os.DirFS(frontend), config)
	watcher := NewWatcher(config, os.DirFS(frontend), engine)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watcher.StartWatching(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := engine.DevURL(); ok {
		t.Fatal("engine reported dev mode before the hot file existed")
	}

	hotFile := filepath.Join(frontend, ".vite", "hot")
	if err := os.MkdirAll(filepath.Dir(hotFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hotFile, []byte("http://localhost:5199\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if url, ok := engine.DevURL(); ok {
			if url != "http://localhost:5199" {
				t.Fatalf("dev URL = %q", url)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher never detected the hot file")
}

// An empty read means Vite has created the file but not yet written the URL.
// Treating that as "no dev server" would drop the page back to production tags
// for a frame and serve script tags for assets that have not been built.
func TestRefreshDevURLIgnoresEmptyHotFile(t *testing.T) {
	config := defaultConfig()
	fsys := fstest.MapFS{".vite/hot": {Data: []byte("http://localhost:5173\n")}}
	engine := newEngine(fsys, config)
	watcher := NewWatcher(config, fsys, engine)

	watcher.refreshDevURL()
	if _, ok := engine.DevURL(); !ok {
		t.Fatal("expected dev mode")
	}

	watcher.fsys = fstest.MapFS{".vite/hot": {Data: []byte("  \n")}}
	watcher.refreshDevURL()
	if url, ok := engine.DevURL(); !ok || url != "http://localhost:5173" {
		t.Fatalf("a half-written hot file dropped dev mode: %q, %v", url, ok)
	}
}
