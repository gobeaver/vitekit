package vitekit

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// frontendMIME maps file extensions that Go's built-in MIME detector
// gets wrong (or doesn't know about) to the correct Content-Type.
// Without this, browsers with X-Content-Type-Options: nosniff will
// refuse to load the asset.
var frontendMIME = map[string]string{
	".mjs":    "application/javascript",
	".jsx":    "application/javascript",
	".tsx":    "application/javascript",
	".ts":     "application/javascript",
	".vue":    "application/javascript",
	".svelte": "application/javascript",
}

// precompressedSuffixes maps a content coding to the sidecar file a build
// produces for it, best compression first. Serving these avoids paying to
// compress the same immutable asset on every single request.
var precompressedSuffixes = []struct {
	encoding string
	suffix   string
}{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// hashedFileName matches Vite's default "[name]-[hash][ext]" output. It is only
// ever consulted for files the manifest already claims Vite generated, so it
// exists to catch the case where a project has configured hash-free filenames
// — those must still be revalidated on every deploy.
var hashedFileName = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

const (
	immutableCacheControl  = "public, max-age=31536000, immutable"
	revalidateCacheControl = "public, max-age=0, must-revalidate"
)

type assetHandler struct {
	engine *Engine
	assets fs.FS
	files  http.Handler
}

// AssetHandler serves built files below the configured output directory.
// The handler is safe to mount beneath any router that owns the asset prefix.
//
// It fixes MIME types for frontend file extensions that Go's default detector
// misidentifies, sets X-Content-Type-Options: nosniff on every response, serves
// precompressed .br/.gz sidecars when the build produced them, and caches
// content-hashed files permanently while keeping everything else revalidated.
func (e *Engine) AssetHandler() (http.Handler, error) {
	assets, err := fs.Sub(e.fsys, e.outputDir)
	if err != nil {
		return nil, err
	}
	return &assetHandler{
		engine: e,
		assets: assets,
		files:  http.FileServer(http.FS(assets)),
	}, nil
}

func (h *assetHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	clean := "/" + strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	request.URL.Path = clean

	// Fix MIME type for frontend extensions before the file server writes headers.
	extension := path.Ext(clean)
	if mimeType, ok := frontendMIME[extension]; ok {
		writer.Header().Set("Content-Type", mimeType)
	}
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", h.cacheControl(clean))
	// Set on every response, not just compressed ones: a shared cache that
	// stored an unencoded response without this would later hand it to a client
	// that asked for br, and vice versa.
	writer.Header().Add("Vary", "Accept-Encoding")

	if !h.serveable(strings.TrimPrefix(clean, "/")) {
		http.NotFound(writer, request)
		return
	}
	if h.servePrecompressed(writer, request, clean, extension) {
		return
	}
	h.files.ServeHTTP(writer, request)
}

// serveable rejects the two things a build directory should never hand out:
// Vite's own build metadata, and a directory listing that enumerates the whole
// deployment for anyone who asks. Other dot-paths are left alone, because
// public/.well-known is a legitimate thing to ship.
func (h *assetHandler) serveable(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if segment == ".vite" {
			return false
		}
	}
	target := name
	if target == "" {
		target = "."
	}
	info, err := fs.Stat(h.assets, target)
	if err != nil {
		return true // let the file server produce the 404
	}
	return !info.IsDir()
}

// cacheControl decides how long a built file may be cached. Vite puts a content
// hash in the filename of everything it generates, so those URLs can never
// change meaning and are safe to cache forever. Files copied verbatim from
// public/ keep their names across deploys and must be revalidated.
func (h *assetHandler) cacheControl(urlPath string) string {
	name := strings.TrimPrefix(urlPath, "/")
	if h.engine.isGeneratedFile(name) && hashedFileName.MatchString(name) {
		return immutableCacheControl
	}
	return revalidateCacheControl
}

func (h *assetHandler) servePrecompressed(writer http.ResponseWriter, request *http.Request, clean, extension string) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	name := strings.TrimPrefix(clean, "/")
	if name == "" || strings.HasSuffix(name, "/") {
		return false
	}
	accepted := acceptedEncodings(request)

	for _, candidate := range precompressedSuffixes {
		if !accepted[candidate.encoding] {
			continue
		}
		file, err := h.assets.Open(name + candidate.suffix)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			_ = file.Close()
			continue
		}
		// ServeContent needs to seek to satisfy Range requests; a filesystem
		// that cannot seek falls through to the plain file server.
		seeker, ok := file.(io.ReadSeeker)
		if !ok {
			_ = file.Close()
			continue
		}

		if writer.Header().Get("Content-Type") == "" {
			if contentType := mime.TypeByExtension(extension); contentType != "" {
				writer.Header().Set("Content-Type", contentType)
			}
		}
		writer.Header().Set("Content-Encoding", candidate.encoding)
		// ServeContent would otherwise derive the type from the ".br" suffix.
		http.ServeContent(writer, request, clean, info.ModTime(), seeker)
		_ = file.Close()
		return true
	}
	return false
}

// acceptedEncodings reports which content codings the client will accept,
// honoring an explicit "q=0" rejection.
func acceptedEncodings(request *http.Request) map[string]bool {
	accepted := make(map[string]bool, 2)
	for _, part := range strings.Split(request.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		if name == "" {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			parameter = strings.TrimSpace(parameter)
			if value, ok := strings.CutPrefix(parameter, "q="); ok {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					quality = parsed
				}
			}
		}
		if quality > 0 {
			accepted[name] = true
		}
	}
	return accepted
}
