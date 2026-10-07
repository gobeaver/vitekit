package vitekit

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// watchedPollInterval is a safety net for the events fsnotify can miss —
	// editors that replace files by rename, and network or container
	// filesystems that deliver nothing at all.
	watchedPollInterval = 2 * time.Second

	// unwatchedPollInterval is the fallback cadence when no OS watch could be
	// established, which is the only situation where polling is load-bearing.
	unwatchedPollInterval = 250 * time.Millisecond
)

// Watcher keeps a single Engine's devURL up to date by watching
// for the Vite dev server's .vite/hot marker file. Its only job
// is updating eng.devURL — it holds no state of its own.
type Watcher struct {
	c    *Config
	fsys fs.FS
	eng  *Engine
}

// NewWatcher returns a Watcher that keeps eng in step with the filesystem:
// it moves the engine in and out of development mode as the hot-reload marker
// appears and disappears, and reloads the manifest when a build rewrites it.
// Init and Builder.New create one for you; construct it directly only when
// driving an Engine yourself.
func NewWatcher(c *Config, fsys fs.FS, eng *Engine) *Watcher {
	return &Watcher{c: c, fsys: fsys, eng: eng}
}

// hotOSPath and manifestOSPath translate the filesystem-relative config paths
// into paths on disk. The engine reads through fs.FS, which is rooted at
// RootDir, but fsnotify only understands real OS paths — without this the
// watcher would attach to the wrong directory whenever the frontend lives
// somewhere other than the working directory.
func (w *Watcher) hotOSPath() string {
	return w.osPath(w.c.HotFilePath)
}

func (w *Watcher) manifestOSPath() string {
	return w.osPath(w.c.ManifestPath)
}

func (w *Watcher) osPath(relative string) string {
	if filepath.IsAbs(relative) {
		return filepath.Clean(relative)
	}
	root := w.c.RootDir
	if root == "" {
		root = "."
	}
	return filepath.Join(root, filepath.FromSlash(relative))
}

// StartWatching establishes the watch and does an initial check
// synchronously (Vite may already be running), then watches for
// changes in the background until ctx is canceled.
func (w *Watcher) StartWatching(ctx context.Context) error {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.refreshDevURL() // check current state before waiting for future events

	hotPath := w.hotOSPath()
	manifestPath := w.manifestOSPath()
	hotDir := filepath.Dir(hotPath)
	watching := false

	// Watch the hot-file directory for dev-mode detection. If it does not exist
	// yet, watch its parent so we see it being created.
	if err := fsWatcher.Add(hotDir); err == nil {
		watching = true
	} else if parent := filepath.Dir(hotDir); parent != hotDir {
		if err := fsWatcher.Add(parent); err == nil {
			watching = true
		}
	}

	// Watch the manifest directory for production rolling deploys.
	// When the manifest changes on disk, the engine reloads atomically
	// so in-flight requests keep working while new ones see the update.
	manifestDir := filepath.Dir(manifestPath)
	if manifestDir != hotDir {
		if err := fsWatcher.Add(manifestDir); err == nil { // best-effort; may not exist for embed.FS
			watching = true
		}
	}

	interval := unwatchedPollInterval
	if watching {
		interval = watchedPollInterval
	}

	go func() {
		defer func() { _ = fsWatcher.Close() }()
		poll := time.NewTicker(interval)
		defer poll.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
				w.refreshDevURL()
			case event, ok := <-fsWatcher.Events:
				if !ok {
					return
				}

				// Hot-file directory created (Vite just started).
				if samePath(event.Name, hotDir) && event.Has(fsnotify.Create) {
					_ = fsWatcher.Add(hotDir)
					w.refreshDevURL()
					continue
				}

				// Manifest file changed — reload for rolling deploys.
				if samePath(event.Name, manifestPath) &&
					(event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
					if err := w.eng.reloadManifest(); err != nil {
						log.Println("vitekit: manifest reload failed:", err)
					}
					continue
				}

				// Hot file events.
				if !samePath(event.Name, hotPath) {
					continue
				}
				switch {
				case event.Has(fsnotify.Create), event.Has(fsnotify.Write):
					w.refreshDevURL()
				case event.Has(fsnotify.Remove):
					w.eng.devURL.Store(nil) // back to production mode
				}
			case err, ok := <-fsWatcher.Errors:
				if !ok {
					return
				}
				log.Println("vitekit: watcher error:", err)
			}
		}
	}()

	return nil
}

func samePath(first, second string) bool {
	firstAbs, firstErr := filepath.Abs(first)
	secondAbs, secondErr := filepath.Abs(second)
	if firstErr != nil || secondErr != nil {
		return filepath.Clean(first) == filepath.Clean(second)
	}
	return filepath.Clean(firstAbs) == filepath.Clean(secondAbs)
}

func (w *Watcher) refreshDevURL() {
	// Read relative to w.fsys, consistent with how Engine reads the manifest.
	data, err := fs.ReadFile(w.fsys, filepath.ToSlash(w.c.HotFilePath))
	if err != nil {
		w.eng.devURL.Store(nil)
		return
	}
	url := strings.TrimSpace(string(data))
	if url == "" {
		// Vite writes the file before writing its contents; an empty read means
		// the URL is not known yet, not that the dev server has gone away.
		return
	}
	w.eng.devURL.Store(&url)
}
