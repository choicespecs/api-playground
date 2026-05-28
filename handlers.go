package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// SendRequest handles POST /send
// Reads the form, fires the proxied HTTP request, injects auth, saves history,
// and returns an HTML snippet for HTMX to drop into the response panel.
func SendRequest(c *gin.Context) {
	// ── 1. Read form fields ──────────────────────────────────────────────
	method := c.PostForm("method")
	rawURL := c.PostForm("url")
	body := c.PostForm("body")
	bodyType := c.PostForm("body_type")
	authProfileID := c.PostForm("auth_profile_id")
	collectionID := c.PostForm("collection_id")

	if bodyType == "" {
		bodyType = "json"
	}

	// Header keys and values come in as parallel arrays
	headerKeys := c.PostFormArray("header_key[]")
	headerVals := c.PostFormArray("header_val[]")

	// Same pattern for query params
	paramKeys := c.PostFormArray("param_key[]")
	paramVals := c.PostFormArray("param_val[]")

	// ── 1b. Expand {{VARIABLE}} placeholders (global → env → collection) ──
	rawURL = expandVariablesCtx(rawURL, collectionID)
	body = expandVariablesCtx(body, collectionID)
	for i := range headerVals {
		headerVals[i] = expandVariablesCtx(headerVals[i], collectionID)
	}
	for i := range paramVals {
		paramVals[i] = expandVariablesCtx(paramVals[i], collectionID)
	}

	// ── 1c. Inherit collection's default auth if none explicitly selected ─
	if authProfileID == "" && collectionID != "" {
		if coll, ok := getCollection(collectionID); ok && coll.AuthProfileID != "" {
			authProfileID = coll.AuthProfileID
		}
	}

	// ── 2. Basic validation ──────────────────────────────────────────────
	if rawURL == "" {
		c.HTML(200, "response.html", gin.H{"Error": "Please enter a URL."})
		return
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	// Detect unresolved variable placeholders after expansion — give a clear hint.
	if strings.Contains(rawURL, "{") {
		import_re := regexp.MustCompile(`\{+([A-Za-z_][A-Za-z0-9_]*)\}+`)
		if m := import_re.FindString(rawURL); m != "" {
			name := strings.Trim(m, "{}")
			c.HTML(200, "response.html", gin.H{
				"Error": fmt.Sprintf(
					"URL contains unresolved variable placeholder: %s\n\n"+
						"→ Define \"%s\" in the { } Vars panel, or check the spelling.\n"+
						"→ Use {{%s}} (double braces) or {%s} (single braces) — both work.",
					m, name, name, name),
			})
			return
		}
	}

	// ── 3. Append query params to the URL ────────────────────────────────
	if len(paramKeys) > 0 {
		parsed, err := url.Parse(rawURL)
		if err == nil {
			q := parsed.Query()
			for i, k := range paramKeys {
				if k != "" && i < len(paramVals) {
					q.Set(k, paramVals[i])
				}
			}
			parsed.RawQuery = q.Encode()
			rawURL = parsed.String()
		}
	}

	// ── 4. Build the HTTP request ─────────────────────────────────────────
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		c.HTML(200, "response.html", gin.H{"Error": fmt.Sprintf("Bad request: %s", err.Error())})
		return
	}

	// Attach manually specified headers
	for i, k := range headerKeys {
		if k != "" && i < len(headerVals) {
			req.Header.Set(k, headerVals[i])
		}
	}

	// ── 5. Inject auth profile (overrides any matching manual header) ─────
	if err := injectAuth(req, authProfileID, false); err != nil {
		c.HTML(200, "response.html", gin.H{"Error": fmt.Sprintf("Auth error: %s", err.Error())})
		return
	}

	// ── 6. Fire the request ───────────────────────────────────────────────
	client := &http.Client{Timeout: 30 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)

	if err != nil {
		c.HTML(200, "response.html", gin.H{"Error": fmt.Sprintf("Request failed: %s", err.Error())})
		return
	}

	// ── 6b. Auto-retry on 401 ────────────────────────────────────────────
	// The cached token may have been invalidated server-side before our local
	// ExpiresAt. Force-refresh the token and retry exactly once, transparently.
	if resp.StatusCode == 401 && authProfileID != "" {
		if _, ok := getAuthProfile(authProfileID); ok {
			resp.Body.Close() // done with the original 401 body

			var retryBodyReader io.Reader
			if body != "" {
				retryBodyReader = bytes.NewBufferString(body)
			}
			retryReq, buildErr := http.NewRequest(method, rawURL, retryBodyReader)
			if buildErr != nil {
				c.HTML(200, "response.html", gin.H{"Error": fmt.Sprintf("Retry build failed: %s", buildErr.Error())})
				return
			}
			for i, k := range headerKeys {
				if k != "" && i < len(headerVals) {
					retryReq.Header.Set(k, headerVals[i])
				}
			}
			if authErr := injectAuth(retryReq, authProfileID, true); authErr != nil {
				c.HTML(200, "response.html", gin.H{"Error": fmt.Sprintf("Token refresh failed: %s", authErr.Error())})
				return
			}
			retryStart := time.Now()
			retryResp, retryErr := client.Do(retryReq)
			if retryErr != nil {
				c.HTML(200, "response.html", gin.H{"Error": fmt.Sprintf("Retry after token refresh failed: %s", retryErr.Error())})
				return
			}
			resp = retryResp
			duration = time.Since(retryStart)
		}
	}

	defer resp.Body.Close()

	// ── 7. Read and pretty-print the response body ────────────────────────
	respBytes, _ := io.ReadAll(resp.Body)

	isJSON := false
	formattedBody := string(respBytes)
	var jsonObj interface{}
	if json.Unmarshal(respBytes, &jsonObj) == nil {
		if pretty, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
			formattedBody = string(pretty)
			isJSON = true
		}
	}

	const maxDisplay = 50_000
	truncated := false
	if len(formattedBody) > maxDisplay {
		formattedBody = formattedBody[:maxDisplay]
		truncated = true
	}

	// ── 8. Collect response headers ───────────────────────────────────────
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		respHeaders[k] = strings.Join(v, ", ")
	}

	// ── 9. Human-readable response size ──────────────────────────────────
	size := len(respBytes)
	sizeStr := fmt.Sprintf("%d B", size)
	if size >= 1024 {
		sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
	}

	// ── 10. Status badge color ────────────────────────────────────────────
	statusClass := "badge-error"
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		statusClass = "badge-success"
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		statusClass = "badge-warning"
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		statusClass = "badge-error"
	}

	durationStr := fmt.Sprintf("%dms", duration.Milliseconds())

	// ── 11. Save to history ───────────────────────────────────────────────
	var sentHeaders []KVPair
	for i, k := range headerKeys {
		if k != "" && i < len(headerVals) {
			sentHeaders = append(sentHeaders, KVPair{k, headerVals[i]})
		}
	}
	var sentParams []KVPair
	for i, k := range paramKeys {
		if k != "" && i < len(paramVals) {
			sentParams = append(sentParams, KVPair{k, paramVals[i]})
		}
	}
	entry := newHistoryEntry(
		method, rawURL,
		sentHeaders, sentParams,
		body, bodyType, authProfileID,
		resp.StatusCode, durationStr,
	)
	addToHistory(entry)

	c.Header("HX-Trigger", "historyUpdated")

	// ── 12. 401 — suggest Token API profiles if any exist ────────────────
	var suggestedProfiles []AuthProfile
	if resp.StatusCode == 401 {
		suggestedProfiles = loadAuthProfiles()
	}

	// ── 13. Render the response panel ─────────────────────────────────────
	c.HTML(200, "response.html", gin.H{
		"StatusCode":        resp.StatusCode,
		"StatusText":        resp.Status,
		"StatusClass":       statusClass,
		"Duration":          durationStr,
		"Size":              sizeStr,
		"Headers":           respHeaders,
		"Body":              formattedBody,
		"IsJSON":            isJSON,
		"Truncated":         truncated,
		"URL":               rawURL,
		"SuggestedProfiles": suggestedProfiles,
	})
}
