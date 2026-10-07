package vitekit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// A manifest is build output, but it is a file: a broken build, a truncated
// upload or a compromised toolchain can all put arbitrary bytes there. Whatever
// it contains, the engine must fail rather than panic in a request handler.
func FuzzManifestNeverPanics(f *testing.F) {
	f.Add([]byte(`{"src/main.js":{"file":"assets/main-AAAAAAAA.js","isEntry":true}}`))
	f.Add([]byte(`{"src/main.js":{"file":"a.js","css":["b.css"],"imports":["_c.js"]}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"a":{"file":`))
	f.Add([]byte("\x00\xff"))

	f.Fuzz(func(t *testing.T, manifest []byte) {
		fsys := fstest.MapFS{"dist/.vite/manifest.json": {Data: manifest}}
		engine := newEngine(fsys, defaultConfig())
		engine.manifestPath = "dist/.vite/manifest.json"

		// An error is a fine outcome; a panic is not.
		if err := engine.reloadManifest(); err != nil {
			return
		}
		_, _ = engine.Tags("src/main.js")
		_, _ = engine.TagsFor([]string{"src/main.js", "missing.js"}, RenderOptions{})
		_, _ = engine.Preloads("src/main.js")
		_, _ = engine.Asset("src/main.js")
		_ = engine.ContentSecurityPolicy("nonce")
	})
}

// Filenames reach the browser inside HTML attributes. Anything that arrives in
// the manifest must be escaped on the way out, or a poisoned build turns into
// script execution on every page.
func FuzzTagsEscapeManifestFilenames(f *testing.F) {
	f.Add(`assets/main.js`)
	f.Add(`assets/x"><script>alert(1)</script>.js`)
	f.Add(`"><img src=x onerror=alert(1)>`)
	f.Add(`a'b"c<d>e&f`)

	f.Fuzz(func(t *testing.T, file string) {
		encoded, err := jsonString(file)
		if err != nil {
			return
		}
		fsys := fstest.MapFS{"dist/.vite/manifest.json": {
			Data: []byte(`{"src/main.js":{"file":` + encoded + `,"isEntry":true}}`),
		}}
		engine := newEngine(fsys, defaultConfig())
		engine.manifestPath = "dist/.vite/manifest.json"
		if err := engine.reloadManifest(); err != nil {
			return
		}
		tags, err := engine.Tags("src/main.js")
		if err != nil {
			return
		}
		// Everything the engine emits itself is a tag it wrote; nothing that
		// came out of the manifest may add one of its own.
		body := strings.TrimPrefix(tags, `<script type="module" src="`)
		if index := strings.LastIndex(body, `"></script>`); index >= 0 {
			body = body[:index]
		}
		for _, forbidden := range []string{"<", ">", `"`} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("unescaped %q reached the attribute from %q:\n%s", forbidden, file, tags)
			}
		}
	})
}

// The asset handler is the only part of vitekit that turns a request path into
// a file read. No path may reach anything outside the build directory.
func FuzzAssetHandlerStaysInsideTheOutputDirectory(f *testing.F) {
	f.Add("/assets/main-AAAAAAAA.js")
	f.Add("/../secret.env")
	f.Add("/..%2fsecret.env")
	f.Add("/assets/../../secret.env")
	f.Add("/./.././secret.env")
	f.Add("//secret.env")
	f.Add("/.vite/manifest.json")

	const secret = "SECRET-VALUE-MUST-NOT-ESCAPE"
	fsys := fstest.MapFS{
		"dist/.vite/manifest.json":     {Data: []byte(`{"src/main.js":{"file":"assets/main-AAAAAAAA.js","isEntry":true}}`)},
		"dist/assets/main-AAAAAAAA.js": {Data: []byte("ok")},
		"secret.env":                   {Data: []byte(secret)},
		"dist/../secret-sibling.env":   {Data: []byte(secret)},
	}
	engine := newEngine(fsys, defaultConfig())
	engine.manifestPath = "dist/.vite/manifest.json"
	if err := engine.reloadManifest(); err != nil {
		f.Fatal(err)
	}
	handler, err := engine.AssetHandler()
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, target string) {
		if !strings.HasPrefix(target, "/") {
			target = "/" + target
		}
		request, err := http.NewRequest(http.MethodGet, "http://example.test"+target, nil)
		if err != nil {
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("path %q escaped the output directory (status %d)", target, recorder.Code)
		}
		if strings.Contains(strings.ToLower(recorder.Body.String()), "manifest.json") &&
			recorder.Code == http.StatusOK {
			t.Fatalf("path %q exposed build metadata", target)
		}
	})
}

// The port is appended to a command a user typed. Whatever they typed, the
// result must stay a single well-formed command.
func FuzzCommandWithVitePort(f *testing.F) {
	f.Add("npm run dev", 5173)
	f.Add("vite", 3000)
	f.Add("pnpm dev --host", 4000)
	f.Add("yarn dev --port 9999", 5173)
	f.Add("", 0)

	f.Fuzz(func(t *testing.T, command string, port int) {
		if port < 0 || port > 65535 {
			return
		}
		result := commandWithVitePort(command, port)
		// The function may only append; it must never introduce a line break
		// that would turn one command into two for the shell.
		if !strings.Contains(command, "\n") && strings.Contains(result, "\n") {
			t.Fatalf("port injection introduced a newline: %q -> %q", command, result)
		}
		// The security-relevant invariant: appending a port may never change
		// which program the shell ends up running.
		if program(result) != program(command) {
			t.Fatalf("port injection changed the program: %q -> %q", command, result)
		}
		// A command that already pins a port must be left exactly as it was.
		if hasPortFlag(command) && result != command {
			t.Fatalf("rewrote a command that already pins a port: %q -> %q", command, result)
		}
	})
}

// program is the first word of a command — the executable the shell resolves.
func program(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// jsonString quotes a value the way a manifest would carry it, skipping inputs
// that cannot appear in JSON at all.
func jsonString(value string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
