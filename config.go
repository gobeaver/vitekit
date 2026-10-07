package vitekit

import (
	"context"
	"fmt"
	"io/fs"
	"os"
)

// Config holds vitekit's boot-time settings — everything here is
// read once at Init() and treated as immutable afterward. Live
// runtime state (the loaded manifest, the current dev server URL)
// lives on Engine, not here.
type Config struct {
	ManifestPath         string // e.g. ".vite/manifest.json"
	OutputDir            string // e.g. "dist"
	DevCommand           string // e.g. "npm run dev"
	HotFilePath          string // path to the hot-reload marker, relative to the fs.FS
	AssetsPrefix         string // URL prefix the output directory is mounted at; default "/"
	Entry                string // optional default entry point
	RootDir              string // where the fs.FS is rooted on disk; used only for file watching
	AllowMissingManifest bool   // allow dev-only startup before Vite writes its manifest

	// outputDirExplicit distinguishes a caller-chosen output directory from
	// the default, so manifest discovery knows whether it may infer one.
	outputDirExplicit bool
}

// Environment variables are read under the VITEKIT_ prefix. The BEAVER_VITE_
// prefix is still honored as a fallback for applications that adopted vitekit
// as part of Beaver Kit before it was published on its own.
const (
	envPrefix       = "VITEKIT_"
	legacyEnvPrefix = "BEAVER_VITE_"
)

func defaultConfig() *Config {
	return &Config{
		ManifestPath: envOr("MANIFEST_PATH", ".vite/manifest.json", envPrefix, legacyEnvPrefix),
		OutputDir:    envOr("OUTPUT_DIR", "dist", envPrefix, legacyEnvPrefix),
		DevCommand:   envOr("DEV_COMMAND", "npm run dev", envPrefix, legacyEnvPrefix),
		HotFilePath:  envOr("HOT_FILE", ".vite/hot", envPrefix, legacyEnvPrefix),
		AssetsPrefix: envOr("ASSETS_PREFIX", "/", envPrefix, legacyEnvPrefix),
		Entry:        envOr("ENTRY", "", envPrefix, legacyEnvPrefix),
		RootDir:      envOr("ROOT_DIR", ".", envPrefix, legacyEnvPrefix),

		outputDirExplicit: envOr("OUTPUT_DIR", "", envPrefix, legacyEnvPrefix) != "",
	}
}

// envOr returns the first non-empty value among the given prefixes, falling
// back to the supplied default.
func envOr(name, fallback string, prefixes ...string) string {
	for _, prefix := range prefixes {
		if value := os.Getenv(prefix + name); value != "" {
			return value
		}
	}
	return fallback
}

// Option customizes Config during Init.
type Option func(*Config)

// WithManifestPath overrides where the Vite manifest is read from, relative to
// the fs.FS. Rarely needed: an unset path is discovered, which is what makes an
// embedded build work with no configuration at all.
func WithManifestPath(path string) Option {
	return func(c *Config) { c.ManifestPath = path }
}

// WithOutputDir overrides the build output directory that AssetHandler serves,
// relative to the fs.FS. It is otherwise inferred from where the manifest was
// found, so setting it is only necessary for a layout that inference gets wrong.
func WithOutputDir(dir string) Option {
	return func(c *Config) {
		c.OutputDir = dir
		c.outputDirExplicit = true
	}
}

// WithDevCommand sets the command that starts the Vite dev server. It is used
// by DevOrchestrator; the engine itself never runs it.
func WithDevCommand(cmd string) Option {
	return func(c *Config) { c.DevCommand = cmd }
}

// WithHotFilePath overrides the marker file whose presence puts the engine in
// development mode, relative to the fs.FS. Change it only if you have also
// changed where the dev server writes it.
func WithHotFilePath(path string) Option {
	return func(c *Config) { c.HotFilePath = path }
}

// WithAssetsPrefix sets the URL prefix that the build output directory is
// served under. It must match where AssetHandler is mounted.
//
// The default is "/", which reproduces Vite's own layout: a chunk written to
// dist/assets/main-BsO1RtEz.js is referenced as /assets/main-BsO1RtEz.js, and
// the handler is mounted with no path stripping:
//
//	mux.Handle("/assets/", assetHandler)
//
// Behind a reverse proxy that serves the app from a subdirectory, set this to
// that subdirectory — WithAssetsPrefix("/app/") yields /app/assets/main-*.js.
func WithAssetsPrefix(prefix string) Option {
	return func(c *Config) { c.AssetsPrefix = prefix }
}

// WithEntry sets a default entry point, so callers that always render the same
// one need not repeat it. Tags and its siblings still take an explicit entry.
func WithEntry(entry string) Option {
	return func(c *Config) { c.Entry = entry }
}

// WithRootDir tells vitekit where the fs.FS passed to Init is rooted on disk.
// The engine itself always reads through the fs.FS; this is what lets the
// watcher attach OS-level notifications to the right directory when the
// frontend does not live in the process's working directory, as in
// os.DirFS("./frontend"). It is irrelevant for embedded filesystems.
func WithRootDir(dir string) Option {
	return func(c *Config) { c.RootDir = dir }
}

// WithDevMode allows initialization before Vite has written a production
// manifest. The watcher will switch to dev mode when .vite/hot appears.
func WithDevMode() Option {
	return func(c *Config) { c.AllowMissingManifest = true }
}

// Builder creates independent Engine instances with a custom
// environment-variable prefix — for apps that run more than one
// Vite build (e.g. admin panel + public site).
//
//	admin, _ := vitekit.WithPrefix("ADMIN_VITE_").New(adminFS)
//	public, _ := vitekit.WithPrefix("PUBLIC_VITE_").New(publicFS)
type Builder struct {
	prefix string
}

// WithPrefix returns a Builder that reads <prefix>* environment variables
// instead of the default VITEKIT_* ones.
func WithPrefix(prefix string) *Builder {
	return &Builder{prefix: prefix}
}

func prefixedConfig(prefix string) *Config {
	// "BEAVER_"+prefix keeps working for Beaver Kit applications that named
	// their variables before vitekit was published standalone.
	legacy := "BEAVER_" + prefix
	return &Config{
		ManifestPath: envOr("MANIFEST_PATH", ".vite/manifest.json", prefix, legacy),
		OutputDir:    envOr("OUTPUT_DIR", "dist", prefix, legacy),
		DevCommand:   envOr("DEV_COMMAND", "npm run dev", prefix, legacy),
		HotFilePath:  envOr("HOT_FILE", ".vite/hot", prefix, legacy),
		AssetsPrefix: envOr("ASSETS_PREFIX", "/", prefix, legacy),
		Entry:        envOr("ENTRY", "", prefix, legacy),
		RootDir:      envOr("ROOT_DIR", ".", prefix, legacy),

		outputDirExplicit: envOr("OUTPUT_DIR", "", prefix, legacy) != "",
	}
}

// New creates a standalone Engine (with its own watcher) using the
// builder's prefix. The returned cancel function stops the watcher.
func (b *Builder) New(fsys fs.FS, opts ...Option) (*Engine, context.CancelFunc, error) {
	cfg := prefixedConfig(b.prefix)
	for _, opt := range opts {
		opt(cfg)
	}

	e := newEngine(fsys, cfg)
	w := NewWatcher(cfg, fsys, e)
	w.refreshDevURL()
	if err := e.reloadManifest(); err != nil && !hasDevURL(e) && !cfg.AllowMissingManifest {
		return nil, nil, fmt.Errorf("vitekit: initial manifest load failed: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.StartWatching(ctx); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("vitekit: failed to start dev-mode watcher: %w", err)
	}
	return e, cancel, nil
}
