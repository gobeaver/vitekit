package vitekit

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

const nonceBytes = 32

// NewNonce returns a cryptographically random base64url nonce suitable for a
// single HTML response's CSP and RenderOptions.
func NewNonce() (string, error) {
	bytes := make([]byte, nonceBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("vitekit: generate nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// CSPHeader returns the minimal CSP value needed to authorize scripts carrying
// the supplied nonce. Callers can append other directives as needed.
//
// Prefer Engine.ContentSecurityPolicy: this value does not account for ES
// module imports, and in development it omits everything the Vite client needs
// in order to connect.
func CSPHeader(nonce string) string {
	return fmt.Sprintf("script-src 'nonce-%s'", nonce)
}

// ContentSecurityPolicy returns a complete Content-Security-Policy value that
// authorizes exactly the tags this engine generates, for whichever mode it is
// currently in. Pass the same nonce to RenderOptions.
//
// Two details make this more than a nonce in a string. A nonce alone does not
// cover the chunks an entry module imports, because those fetches carry no
// nonce of their own — hence 'strict-dynamic', which extends trust from the
// entry to everything it pulls in, with 'self' left in place for browsers that
// do not implement it. And in development the browser must also reach the Vite
// dev server directly: it serves the client and every source module, HMR runs
// over a websocket to it, and its plugin pipeline injects <style> elements that
// no nonce can be attached to.
func (e *Engine) ContentSecurityPolicy(nonce string) string {
	if devURL, ok := e.DevURL(); ok {
		origin := strings.TrimRight(devURL, "/")
		return strings.Join([]string{
			fmt.Sprintf("default-src 'self' %s", origin),
			fmt.Sprintf("script-src 'nonce-%s' 'strict-dynamic' 'self' %s", nonce, origin),
			// Vite injects style elements at runtime; they cannot be nonced.
			fmt.Sprintf("style-src 'self' 'unsafe-inline' %s", origin),
			fmt.Sprintf("connect-src 'self' %s %s", origin, websocketOrigin(origin)),
			fmt.Sprintf("img-src 'self' data: blob: %s", origin),
			fmt.Sprintf("font-src 'self' data: %s", origin),
			"base-uri 'none'",
			"object-src 'none'",
		}, "; ")
	}
	return strings.Join([]string{
		"default-src 'self'",
		fmt.Sprintf("script-src 'nonce-%s' 'strict-dynamic' 'self'", nonce),
		fmt.Sprintf("style-src 'nonce-%s' 'self'", nonce),
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"base-uri 'none'",
		"object-src 'none'",
	}, "; ")
}

// websocketOrigin converts the dev server's HTTP origin into the ws:// or
// wss:// origin its HMR channel actually connects to. connect-src matches on
// scheme, so the http form alone would not authorize the socket.
func websocketOrigin(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := "ws"
	if parsed.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + parsed.Host
}
