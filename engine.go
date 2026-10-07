package vitekit

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"strings"
	"sync"
	"sync/atomic"
)

// ManifestEntry is a single entry in Vite's manifest.json.
// (Named separately from "manifest" — the map of these — to avoid
// confusing a single entry with the whole file.)
type ManifestEntry struct {
	File           string   `json:"file"`
	Src            string   `json:"src"`
	IsEntry        bool     `json:"isEntry"`
	CSS            []string `json:"css"`
	Assets         []string `json:"assets"`
	Imports        []string `json:"imports"`
	DynamicImports []string `json:"dynamicImports"`
}

// Engine holds vitekit's live runtime state: the currently loaded
// manifest and the current dev-server URL (nil when in production
// mode). Both are read/written via atomic.Pointer so concurrent
// requests never block on each other or see a torn read.
type Engine struct {
	fsys      fs.FS
	manifest  atomic.Pointer[map[string]ManifestEntry]
	devURL    atomic.Pointer[string]
	generated atomic.Pointer[map[string]struct{}]

	// reloadMu serializes manifest reloads and guards manifestPath, which the
	// first successful load rewrites to whichever candidate path actually
	// existed. Reads of the manifest itself never take this lock.
	reloadMu     sync.Mutex
	manifestPath string

	assetsPrefix string
	outputDir    string
	entry        string

	// outputDirExplicit records that the caller pinned the output directory,
	// so manifest discovery must not second-guess them.
	outputDirExplicit bool
}

func newEngine(fsys fs.FS, cfg *Config) *Engine {
	return &Engine{
		fsys:              fsys,
		manifestPath:      cfg.ManifestPath,
		assetsPrefix:      cfg.AssetsPrefix,
		outputDir:         cfg.OutputDir,
		entry:             cfg.Entry,
		outputDirExplicit: cfg.outputDirExplicit,
	}
}

func loadManifest(fsys fs.FS, path string) (map[string]ManifestEntry, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m map[string]ManifestEntry
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

func (e *Engine) reloadManifest() error {
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()

	paths := []string{e.manifestPath}
	if e.manifestPath == ".vite/manifest.json" {
		paths = []string{".vite/manifest.json", "dist/.vite/manifest.json", "manifest.json"}
	}
	var lastErr error
	for _, manifestPath := range paths {
		err := e.storeManifestFrom(manifestPath)
		if err == nil {
			return nil
		}
		lastErr = err
	}

	// Nothing at a conventional location. Embedding shifts the whole tree under
	// the embed pattern's own prefix — //go:embed all:frontend/dist puts the
	// manifest at frontend/dist/.vite/manifest.json — so go and find it rather
	// than making every embedded build spell out two extra options.
	if discovered, ok := discoverManifest(e.fsys); ok {
		if err := e.storeManifestFrom(discovered); err == nil {
			if !e.outputDirExplicit {
				e.outputDir = manifestOutputDir(discovered)
			}
			return nil
		}
	}
	return lastErr
}

func (e *Engine) storeManifestFrom(path string) error {
	manifest, err := loadManifest(e.fsys, path)
	if err != nil {
		return err
	}
	e.manifestPath = path
	generated := generatedFiles(manifest)
	// Publish the file set first so a request that sees the new manifest can
	// never miss the caching metadata that goes with it.
	e.generated.Store(&generated)
	e.manifest.Store(&manifest)
	return nil
}

// maxManifestSearchDepth bounds the search so a large embedded tree cannot turn
// startup into a full filesystem walk. A Vite manifest sits at most a couple of
// directories below the embed root in any realistic layout.
const maxManifestSearchDepth = 6

// discoverManifest looks for a Vite manifest anywhere in fsys, preferring the
// shallowest ".vite/manifest.json" it can find. A bare manifest.json is a much
// weaker signal — plenty of unrelated tooling writes one — so it is only used
// when no .vite manifest exists at all.
func discoverManifest(fsys fs.FS) (string, bool) {
	var preferred, fallback string
	_ = fs.WalkDir(fsys, ".", func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir
		}
		if entry.IsDir() {
			// Nothing useful lives in these, and they are enormous.
			if name := entry.Name(); name == "node_modules" || name == ".git" {
				return fs.SkipDir
			}
			if strings.Count(candidate, "/") >= maxManifestSearchDepth {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() != "manifest.json" {
			return nil
		}
		if path.Base(path.Dir(candidate)) == ".vite" {
			if preferred == "" || depth(candidate) < depth(preferred) {
				preferred = candidate
			}
			return nil
		}
		if fallback == "" || depth(candidate) < depth(fallback) {
			fallback = candidate
		}
		return nil
	})

	if preferred != "" {
		return preferred, true
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}

func depth(candidate string) int {
	return strings.Count(candidate, "/")
}

// manifestOutputDir returns the build output directory containing a manifest,
// which is what static assets are served from.
func manifestOutputDir(manifestPath string) string {
	directory := path.Dir(manifestPath)
	if path.Base(directory) == ".vite" {
		directory = path.Dir(directory)
	}
	if directory == "" {
		return "."
	}
	return directory
}

// generatedFiles collects every output file the manifest attributes to Vite.
// Those names carry a content hash, which is what makes them safe to cache
// forever; anything served out of the output directory but absent here (a
// verbatim copy from public/, say) keeps its name across deploys.
func generatedFiles(manifest map[string]ManifestEntry) map[string]struct{} {
	generated := make(map[string]struct{}, len(manifest)*2)
	for _, chunk := range manifest {
		if chunk.File != "" {
			generated[chunk.File] = struct{}{}
		}
		for _, css := range chunk.CSS {
			generated[css] = struct{}{}
		}
		for _, asset := range chunk.Assets {
			generated[asset] = struct{}{}
		}
	}
	return generated
}

// isGeneratedFile reports whether the manifest claims Vite produced this exact
// output file.
func (e *Engine) isGeneratedFile(name string) bool {
	generated := e.generated.Load()
	if generated == nil {
		return false
	}
	_, ok := (*generated)[name]
	return ok
}

func (e *Engine) lookup(entry string) (ManifestEntry, bool) {
	m := e.manifest.Load()
	if m == nil {
		return ManifestEntry{}, false
	}
	v, ok := (*m)[entry]
	return v, ok
}

// --- package-level Init / Service / Reset, matching beaver-kit's convention ---

var (
	// globalEngine is read by Service on every request but written only by
	// Init and Reset, so it is an atomic pointer rather than a mutex-guarded
	// variable: a plain read would race with a concurrent Init.
	globalEngine atomic.Pointer[Engine]
	cancelFn     context.CancelFunc
	initMu       sync.Mutex
)

// Init sets up the engine against fsys (a real os.DirFS in dev,
// potentially an embed.FS in production) and starts the dev-mode
// watcher. Safe to call once at application startup.
func Init(fsys fs.FS, opts ...Option) error {
	initMu.Lock()
	defer initMu.Unlock()

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	e := newEngine(fsys, cfg)
	w := NewWatcher(cfg, fsys, e)
	w.refreshDevURL()
	if err := e.reloadManifest(); err != nil && !hasDevURL(e) && !cfg.AllowMissingManifest {
		return fmt.Errorf("vitekit: initial manifest load failed: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.StartWatching(ctx); err != nil {
		cancel()
		return fmt.Errorf("vitekit: failed to start dev-mode watcher: %w", err)
	}

	globalEngine.Store(e)
	cancelFn = cancel
	return nil
}

func hasDevURL(e *Engine) bool {
	_, ok := e.DevURL()
	return ok
}

// Service returns the initialized Engine. Panics if Init hasn't
// been called — same contract as the rest of beaver-kit.
func Service() *Engine {
	engine := globalEngine.Load()
	if engine == nil {
		panic("vitekit: Service() called before Init()")
	}
	return engine
}

// Reset tears down global state — for test teardown only.
func Reset() {
	initMu.Lock()
	defer initMu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	globalEngine.Store(nil)
	cancelFn = nil
}

// DevURL reports the active Vite development server URL, if one was detected.
func (e *Engine) DevURL() (string, bool) {
	url := e.devURL.Load()
	if url == nil || *url == "" {
		return "", false
	}
	return *url, true
}

// Tags resolves all static dependencies of an entry using the global engine.
func Tags(entry string) (string, error) {
	return Service().Tags(entry)
}

// TagsWithOptions resolves an entry with options using the global engine.
func TagsWithOptions(entry string, options RenderOptions) (string, error) {
	return Service().TagsWithOptions(entry, options)
}

// Preloads returns the resolved asset URLs for all code-split JS chunks of an entry.
func Preloads(entry string) ([]string, error) {
	return Service().Preloads(entry)
}

// Asset resolves a logical asset name using the global engine.
func Asset(name string) (string, bool) {
	return Service().Asset(name)
}

// FuncMap exposes vitekit template helpers using the global engine.
func FuncMap(options RenderOptions) template.FuncMap {
	return Service().FuncMap(options)
}
