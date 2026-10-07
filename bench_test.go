package vitekit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// benchEngine is a loaded engine over a small but realistic build: an entry with
// its own CSS and a shared vendor chunk.
func benchEngine(b *testing.B) *Engine {
	b.Helper()
	engine := newEngine(manifestVersion("AAAAAAAA"), defaultConfig())
	engine.manifestPath = "dist/.vite/manifest.json"
	if err := engine.reloadManifest(); err != nil {
		b.Fatal(err)
	}
	return engine
}

// Tags runs on every rendered page, so a regression here is felt on every
// request rather than at startup.
func BenchmarkTags(b *testing.B) {
	engine := benchEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := engine.Tags("src/main.js"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTagsWithNonce(b *testing.B) {
	engine := benchEngine(b)
	options := RenderOptions{Nonce: "dGVzdC1ub25jZS12YWx1ZQ"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := engine.TagsWithOptions("src/main.js", options); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPreloads(b *testing.B) {
	engine := benchEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := engine.Preloads("src/main.js"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkContentSecurityPolicy(b *testing.B) {
	engine := benchEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = engine.ContentSecurityPolicy("dGVzdC1ub25jZS12YWx1ZQ")
	}
}

// The asset handler is the other per-request path: every script, stylesheet and
// image on the page goes through it.
func BenchmarkAssetHandler(b *testing.B) {
	engine := benchEngine(b)
	handler, err := engine.AssetHandler()
	if err != nil {
		b.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/assets/main-AAAAAAAA.js", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
	}
}

// Rendering is meant to be safe under concurrency without locking readers out;
// this is the shape a real server puts it in.
func BenchmarkTagsParallel(b *testing.B) {
	engine := benchEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := engine.Tags("src/main.js"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
