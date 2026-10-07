package vitekit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// readerStartTimeout bounds the wait for the first reader so a scheduling
// failure reports itself instead of hanging until the test binary times out.
const readerStartTimeout = 30 * time.Second

// manifestVersion builds a manifest whose filenames change with each version,
// standing in for a rolling deploy rewriting the build output underneath a
// running server.
func manifestVersion(version string) fstest.MapFS {
	return fstest.MapFS{
		"dist/.vite/manifest.json": {Data: []byte(`{
			"src/main.js":{"file":"assets/main-` + version + `.js","isEntry":true,
				"css":["assets/main-` + version + `.css"],"imports":["_vendor-` + version + `.js"]},
			"_vendor-` + version + `.js":{"file":"assets/vendor-` + version + `.js"}
		}`)},
		"dist/assets/main-" + version + ".js":   {Data: []byte("main")},
		"dist/assets/vendor-" + version + ".js": {Data: []byte("vendor")},
		"dist/assets/main-" + version + ".css":  {Data: []byte("css")},
	}
}

// The whole point of holding the manifest in an atomic pointer is that a
// reload during a rolling deploy neither blocks readers nor lets one observe a
// half-updated view. This hammers both at once under the race detector: every
// render must come back internally consistent — all three URLs from the same
// build, never a mix of two.
func TestManifestReloadIsAtomicUnderLoad(t *testing.T) {
	versions := []string{"AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"}

	engine := newEngine(manifestVersion(versions[0]), defaultConfig())
	engine.manifestPath = "dist/.vite/manifest.json"
	if err := engine.reloadManifest(); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var waitGroup sync.WaitGroup
	var renders atomic.Int64

	// Nothing schedules the readers before the writer, so the writer waits for
	// a first render. Otherwise it can run all 200 reloads against an idle
	// engine and the test either proves nothing or fails for want of readers.
	firstRender := make(chan struct{})
	var renderedOnce sync.Once

	// Readers.
	for i := 0; i < 8; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for !stop.Load() {
				tags, err := engine.Tags("src/main.js")
				if err != nil {
					// A reload swapping the fs underneath can briefly leave no
					// manifest loaded; that is not a torn read.
					continue
				}
				renders.Add(1)
				renderedOnce.Do(func() { close(firstRender) })

				// Every URL in one render must carry the same build version.
				var seen string
				for _, version := range versions {
					if strings.Contains(tags, version) {
						if seen != "" && seen != version {
							t.Errorf("torn render mixed builds %s and %s:\n%s", seen, version, tags)
							return
						}
						seen = version
					}
				}
				if seen == "" {
					t.Errorf("render matched no known build:\n%s", tags)
					return
				}
				if count := strings.Count(tags, seen); count != 3 {
					t.Errorf("expected 3 URLs from build %s, got %d:\n%s", seen, count, tags)
					return
				}
			}
		}()
	}

	// Writer: swap the build and reload, repeatedly.
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		select {
		case <-firstRender:
		case <-time.After(readerStartTimeout):
			t.Error("no render completed before the reloads began")
			stop.Store(true)
			return
		}
		for round := 0; round < 200; round++ {
			version := versions[round%len(versions)]
			engine.fsys = manifestVersion(version)
			if err := engine.reloadManifest(); err != nil {
				t.Errorf("reload failed: %v", err)
				return
			}
		}
		stop.Store(true)
	}()

	waitGroup.Wait()
	if renders.Load() == 0 {
		t.Fatal("no renders completed")
	}
	t.Logf("%d renders across 200 manifest reloads", renders.Load())
}

// Concurrent asset requests and reloads must not race either: the handler
// consults the generated-file set to decide cache headers, and that set is
// replaced alongside the manifest.
func TestAssetHandlerConcurrentWithReload(t *testing.T) {
	engine := newEngine(manifestVersion("AAAAAAAA"), defaultConfig())
	engine.manifestPath = "dist/.vite/manifest.json"
	if err := engine.reloadManifest(); err != nil {
		t.Fatal(err)
	}
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var waitGroup sync.WaitGroup
	var served atomic.Int64

	// As above: without waiting for a first request the reloads can finish
	// before any handler runs, and the test passes having raced nothing.
	firstRequest := make(chan struct{})
	var servedOnce sync.Once

	for i := 0; i < 8; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for !stop.Load() {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/main-AAAAAAAA.js", nil))
				served.Add(1)
				servedOnce.Do(func() { close(firstRequest) })
			}
		}()
	}

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		select {
		case <-firstRequest:
		case <-time.After(readerStartTimeout):
			t.Error("no request was served before the reloads began")
			stop.Store(true)
			return
		}
		for round := 0; round < 200; round++ {
			if err := engine.reloadManifest(); err != nil {
				t.Errorf("reload failed: %v", err)
				return
			}
		}
		stop.Store(true)
	}()

	waitGroup.Wait()
	if served.Load() == 0 {
		t.Fatal("no requests served")
	}
}
