package main

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// KVPair holds a key-value pair used for request headers and query params.
// Both fields are plain strings; encoding/escaping is handled by the caller.
type KVPair struct {
	Key string
	Val string
}

// ParsedCurl holds everything extracted from a raw curl command or raw HTTP block.
// It is the intermediate representation used by both parsers before the data
// is rendered into form.html.
type ParsedCurl struct {
	Method  string
	URL     string
	Headers []KVPair
	Params  []KVPair // query parameters extracted from the URL
	Body    string
}

// tokenize splits a curl string into individual shell tokens.
// It handles:
//   - Single-quoted strings ('value') — content taken literally, no escaping
//   - Double-quoted strings ("value") — backslash escape sequences honoured
//   - Backslash + newline continuations — silently consumed (multi-line curl)
//   - Windows line endings (\r\n) — normalised to \n first
//
// Example input:
//
//	curl -X POST \
//	  https://api.example.com \
//	  -H 'Content-Type: application/json' \
//	  -d '{"key":"val"}'
//
// Example output:
//
//	["curl", "-X", "POST", "https://api.example.com",
//	 "-H", "Content-Type: application/json", "-d", `{"key":"val"}`]
func tokenize(cmd string) []string {
	// Normalize Windows line endings so \r\n behaves like \n everywhere.
	cmd = strings.ReplaceAll(cmd, "\r\n", "\n")
	cmd = strings.ReplaceAll(cmd, "\r", "\n")

	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		// ── Quote toggling ────────────────────────────────────────────────
		case ch == '\'' && !inDouble:
			// Toggle single-quote mode (don't include the quote character itself)
			inSingle = !inSingle

		case ch == '"' && !inSingle:
			// Toggle double-quote mode (don't include the quote character itself)
			inDouble = !inDouble

		// ── Backslash handling ────────────────────────────────────────────
		case ch == '\\' && inDouble:
			// Escape sequence inside double quotes: include the next char literally.
			i++
			if i < len(cmd) {
				current.WriteByte(cmd[i])
			}

		case ch == '\\' && !inSingle && !inDouble:
			// Shell line continuation: backslash immediately before a newline.
			//   curl -X POST \        ← this backslash + newline should vanish
			//     https://example.com
			// Skip both the backslash and the following newline so they produce no token.
			if i+1 < len(cmd) && cmd[i+1] == '\n' {
				i++ // skip the newline; the for-loop increment handles the rest
			} else {
				// Literal backslash in a non-continuation context (e.g. inside a URL path)
				current.WriteByte(ch)
			}

		// ── Whitespace outside quotes = token boundary ────────────────────
		case (ch == ' ' || ch == '\t' || ch == '\n') && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}

		// ── Everything else: accumulate the current token ─────────────────
		default:
			current.WriteByte(ch)
		}
	}

	// Flush any remaining token
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// parseCurl takes a raw curl string and returns a ParsedCurl struct.
//
// Supported flags:
//
//	-X / --request       HTTP method
//	-H / --header        request headers (Key: Value)
//	-d / --data / --data-raw / --data-binary / --data-urlencode  request body
//	-u / --user          basic auth credentials (converted to Authorization: Basic header)
//
// Ignored flags (silently consumed to avoid misidentifying them as the URL):
//
//	--compressed, -s, -S, -L, -v, -i, -k, --insecure, --silent, --location,
//	--include, --verbose, --no-buffer
//
// The URL is the first non-flag token. Query parameters are extracted from the
// URL into Params[] via url.Parse so that the form's Params tab is populated.
// If a body is present and no method was specified, the method defaults to POST.
func parseCurl(cmd string) ParsedCurl {
	tokens := tokenize(cmd)
	result := ParsedCurl{Method: "GET"}

	i := 0
	if len(tokens) > 0 && tokens[0] == "curl" {
		i = 1
	}

	for i < len(tokens) {
		tok := tokens[i]

		switch tok {
		case "-X", "--request":
			i++
			if i < len(tokens) {
				result.Method = strings.ToUpper(tokens[i])
			}

		case "-H", "--header":
			i++
			if i < len(tokens) {
				parts := strings.SplitN(tokens[i], ": ", 2)
				if len(parts) == 2 {
					result.Headers = append(result.Headers, KVPair{parts[0], parts[1]})
				} else {
					result.Headers = append(result.Headers, KVPair{tokens[i], ""})
				}
			}

		case "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode":
			i++
			if i < len(tokens) {
				result.Body = tokens[i]
			}

		case "-u", "--user":
			// Basic auth: base64-encode "user:pass" and inject as Authorization header
			i++
			if i < len(tokens) {
				encoded := base64.StdEncoding.EncodeToString([]byte(tokens[i]))
				result.Headers = append(result.Headers, KVPair{
					Key: "Authorization",
					Val: "Basic " + encoded,
				})
			}

		case "--compressed", "-s", "-S", "-L", "-v", "-i",
			"-k", "--insecure", "--silent", "--location",
			"--include", "--verbose", "--no-buffer":
			// skip — these flags have no equivalent in the request form

		default:
			if !strings.HasPrefix(tok, "-") && result.URL == "" {
				result.URL = tok
			}
		}
		i++
	}

	// Default to POST when a body is present but no method was specified
	if result.Body != "" && result.Method == "GET" {
		result.Method = "POST"
	}

	// Extract query params from the URL and strip them (they live in Params now)
	if result.URL != "" {
		parsed, err := url.Parse(result.URL)
		if err == nil && parsed.RawQuery != "" {
			for k, vals := range parsed.Query() {
				result.Params = append(result.Params, KVPair{
					Key: k,
					Val: strings.Join(vals, ", "),
				})
			}
			parsed.RawQuery = ""
			result.URL = parsed.String()
		}
	}

	return result
}

// ── Raw HTTP parser ────────────────────────────────────────────────────────

// parseRawHTTP parses a raw HTTP request (as copied from browser DevTools,
// Charles, Wireshark, or similar tools) into a ParsedCurl struct.
//
// Supports both request-target forms:
//
//	GET /path?q=1 HTTP/1.1          relative target (requires Host header)
//	POST https://host/path HTTP/1.1 absolute target
//	POST https://host/path          without HTTP version line
//
// The Host header is consumed to reconstruct the full URL when the target is
// relative. For localhost/127.x/192.168.x addresses the scheme defaults to
// http://; all others default to https://.
//
// Query parameters are extracted into Params[] and removed from the URL.
// The body is everything after the first blank line (standard HTTP message format).
func parseRawHTTP(raw string) ParsedCurl {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	result := ParsedCurl{Method: "GET"}

	if len(lines) == 0 {
		return result
	}

	// ── First line: METHOD target [HTTP/version] ──────────────────────────
	firstParts := strings.Fields(strings.TrimSpace(lines[0]))
	if len(firstParts) == 0 {
		return result
	}
	result.Method = strings.ToUpper(firstParts[0])
	requestTarget := ""
	if len(firstParts) >= 2 {
		requestTarget = firstParts[1] // may be absolute URL or /path
	}

	// ── Headers until blank line ──────────────────────────────────────────
	host := ""
	bodyStart := len(lines)

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			bodyStart = i + 1
			break
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if strings.EqualFold(k, "host") {
			host = v
		} else {
			result.Headers = append(result.Headers, KVPair{k, v})
		}
	}

	// ── Build full URL ────────────────────────────────────────────────────
	fullURL := requestTarget
	if !strings.HasPrefix(requestTarget, "http://") && !strings.HasPrefix(requestTarget, "https://") {
		// Relative target — need Host header
		if host != "" {
			scheme := "https://"
			h := strings.Split(host, ":")[0] // strip port for scheme decision
			if h == "localhost" || strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "192.168.") {
				scheme = "http://"
			}
			fullURL = scheme + host + requestTarget
		}
	}

	// Extract query params from the URL
	if fullURL != "" {
		parsed, err := url.Parse(fullURL)
		if err == nil {
			if parsed.RawQuery != "" {
				for k, vals := range parsed.Query() {
					result.Params = append(result.Params, KVPair{k, strings.Join(vals, ", ")})
				}
				parsed.RawQuery = ""
			}
			result.URL = parsed.String()
		} else {
			result.URL = fullURL
		}
	}

	// ── Body ─────────────────────────────────────────────────────────────
	if bodyStart < len(lines) {
		result.Body = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	}

	// Default to POST when a body is present but no method was specified
	if result.Body != "" && result.Method == "GET" {
		result.Method = "POST"
	}

	return result
}

// bodyTypeFromHeaders infers the body_type select value from a Content-Type header.
// Returns one of: "json", "xml", "form", "text".
// Falls back to "json" if no Content-Type header is present or the value is unrecognised.
func bodyTypeFromHeaders(headers []KVPair) string {
	for _, h := range headers {
		if strings.EqualFold(h.Key, "content-type") {
			v := strings.ToLower(h.Val)
			switch {
			case strings.Contains(v, "xml"):
				return "xml"
			case strings.Contains(v, "form"):
				return "form"
			case strings.Contains(v, "text/plain"):
				return "text"
			default:
				return "json"
			}
		}
	}
	return "json"
}

// ParseRawHTTPHandler handles POST /parse-raw-http.
// Parses the raw_http form field and returns a pre-filled form.html.
// The minimum header row count is 2; at least 1 param row is always present.
func ParseRawHTTPHandler(c *gin.Context) {
	raw := strings.TrimSpace(c.PostForm("raw_http"))
	if raw == "" {
		c.HTML(200, "form.html", defaultFormData())
		return
	}

	parsed := parseRawHTTP(raw)

	for len(parsed.Headers) < 2 {
		parsed.Headers = append(parsed.Headers, KVPair{"", ""})
	}
	if len(parsed.Params) == 0 {
		parsed.Params = append(parsed.Params, KVPair{"", ""})
	}

	c.HTML(200, "form.html", gin.H{
		"Method":        parsed.Method,
		"URL":           parsed.URL,
		"Headers":       parsed.Headers,
		"Params":        parsed.Params,
		"Body":          parsed.Body,
		"BodyType":      bodyTypeFromHeaders(parsed.Headers),
		"AuthProfileID": "",
	})
}

// ParseCurlHandler handles POST /parse-curl.
// Parses the curl_command form field and returns a pre-filled form.html.
// The minimum header row count is 2; at least 1 param row is always present.
func ParseCurlHandler(c *gin.Context) {
	curlCmd := strings.TrimSpace(c.PostForm("curl_command"))

	if curlCmd == "" {
		c.HTML(200, "form.html", defaultFormData())
		return
	}

	parsed := parseCurl(curlCmd)

	for len(parsed.Headers) < 2 {
		parsed.Headers = append(parsed.Headers, KVPair{"", ""})
	}
	if len(parsed.Params) == 0 {
		parsed.Params = append(parsed.Params, KVPair{"", ""})
	}

	c.HTML(200, "form.html", gin.H{
		"Method":        parsed.Method,
		"URL":           parsed.URL,
		"Headers":       parsed.Headers,
		"Params":        parsed.Params,
		"Body":          parsed.Body,
		"BodyType":      bodyTypeFromHeaders(parsed.Headers),
		"AuthProfileID": "",
	})
}

// defaultFormData returns the blank form state used on first load and as
// a fallback when history/collection lookups fail. It provides sensible
// defaults: GET method, empty URL, two blank header rows, one blank param row,
// JSON body type, and no auth profile.
func defaultFormData() gin.H {
	return gin.H{
		"Method":        "GET",
		"URL":           "",
		"Headers":       []KVPair{{"", ""}, {"", ""}},
		"Params":        []KVPair{{"", ""}},
		"Body":          "",
		"BodyType":      "json",
		"AuthProfileID": "",
	}
}
