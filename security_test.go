package vitekit

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestContentSecurityPolicyProduction(t *testing.T) {
	engine := newEngine(fstest.MapFS{}, defaultConfig())
	policy := engine.ContentSecurityPolicy("abc123")

	for _, want := range []string{
		// A nonce alone does not authorize the chunks an entry module imports.
		"script-src 'nonce-abc123' 'strict-dynamic' 'self'",
		"style-src 'nonce-abc123' 'self'",
		"base-uri 'none'",
		"object-src 'none'",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy missing %q: %s", want, policy)
		}
	}
	if strings.Contains(policy, "localhost") || strings.Contains(policy, "unsafe-inline") {
		t.Errorf("production policy leaked development permissions: %s", policy)
	}
}

func TestContentSecurityPolicyDevMode(t *testing.T) {
	engine := newEngine(fstest.MapFS{}, defaultConfig())
	devURL := "http://localhost:5173"
	engine.devURL.Store(&devURL)

	policy := engine.ContentSecurityPolicy("abc123")
	for _, want := range []string{
		"script-src 'nonce-abc123' 'strict-dynamic' 'self' http://localhost:5173",
		// The HMR socket is matched by scheme, so the http origin alone would
		// not authorize it.
		"ws://localhost:5173",
		// Vite injects <style> elements that cannot carry a nonce.
		"'unsafe-inline'",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("dev policy missing %q: %s", want, policy)
		}
	}
}

func TestWebsocketOrigin(t *testing.T) {
	cases := map[string]string{
		"http://localhost:5173": "ws://localhost:5173",
		"https://dev.test:5173": "wss://dev.test:5173",
		"http://127.0.0.1:3000": "ws://127.0.0.1:3000",
		"not a url":             "",
	}
	for origin, want := range cases {
		if got := websocketOrigin(origin); got != want {
			t.Errorf("websocketOrigin(%q) = %q, want %q", origin, got, want)
		}
	}
}
