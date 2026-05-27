package main

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// KVPair holds a key-value pair (used for headers and query params)
type KVPair struct {
	Key string
	Val string
}

// ParsedCurl holds everything extracted from a raw curl command
type ParsedCurl struct {
	Method  string
	URL     string
	Headers []KVPair
	Params  []KVPair
	Body    string
}

// tokenize splits a curl string into individual tokens,
// correctly handling single-quoted and double-quoted strings,
// shell line continuations (backslash + newline), and Windows line endings.
//
// Example:
//
//	curl -X POST \
//	  https://api.example.com \
//	  -H 'Content-Type: application/json' \
//	  -d '{"key":"val"}'
//
// becomes:
//
//	["curl", "-X", "POST", "https://api.example.com", "-H", "Content-Type: application/json", "-d", `{"key":"val"}`]
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
// It handles the most common curl flags:
//
//	-X / --request       → HTTP method
//	-H / --header        → request headers
//	-d / --data / etc.   → request body
//	-u / --user          → basic auth (converted to Authorization header)
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
			// skip

		default:
			if !strings.HasPrefix(tok, "-") && result.URL == "" {
				result.URL = tok
			}
		}
		i++
	}

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
// Charles, Wireshark, etc.) into a ParsedCurl struct.
//
// Supports both request-target forms:
//
//	GET /path?q=1 HTTP/1.1          ← relative target + Host header
//	POST https://host/path HTTP/1.1 ← absolute target
//	POST https://host/path          ← no HTTP version line
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

	if result.Body != "" && result.Method == "GET" {
		result.Method = "POST"
	}

	return result
}

// ParseRawHTTPHandler handles POST /parse-raw-http
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
		"BodyType":      "json",
		"AuthProfileID": "",
	})
}

// ParseCurlHandler handles POST /parse-curl
// Parses the curl string and returns the pre-filled request form HTML.
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
		"BodyType":      "json",
		"AuthProfileID": "",
	})
}

// defaultFormData returns the blank form state (used on first load and fallbacks).
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
