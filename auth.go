package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const authFile = "auth_profiles.json"

// AuthProfile stores the configuration for a Token API auth flow:
// call a login endpoint, extract a token from the JSON response,
// cache it, and inject it into every subsequent request automatically.
// Simple static auth (bearer tokens, API keys, etc.) should be handled
// via {{VARIABLE}} placeholders in request headers instead.
type AuthProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // always "login"

	// Step 1 — login request
	LoginMethod     string `json:"login_method,omitempty"`      // GET | POST (default POST)
	LoginURL        string `json:"login_url,omitempty"`         // e.g. https://api.example.com/auth/login
	LoginBodyType   string `json:"login_body_type,omitempty"`   // json | form
	LoginBody       string `json:"login_body,omitempty"`        // credentials body
	LoginHeadersRaw string `json:"login_headers_raw,omitempty"` // "Key: Value" per line

	// Step 2 — extract token from JSON response (dot-notation path)
	TokenPath  string `json:"token_path,omitempty"`  // e.g. "access_token" or "data.token"
	ExpiryPath string `json:"expiry_path,omitempty"` // integer seconds field, e.g. "expires_in"

	// Step 3 — inject token into downstream requests
	TokenHeaderName string `json:"token_header_name,omitempty"` // default: Authorization
	TokenPrefix     string `json:"token_prefix,omitempty"`      // default: "Bearer "

	// Cached token (written back to disk after each fetch)
	AccessToken string `json:"access_token,omitempty"`
	ExpiresAt   int64  `json:"expires_at,omitempty"` // Unix timestamp

	CreatedAt string `json:"created_at"`
}

// ── Persistence ────────────────────────────────────────────────────────────

func loadAuthProfiles() []AuthProfile {
	data, err := os.ReadFile(authFile)
	if err != nil {
		return []AuthProfile{}
	}
	var ps []AuthProfile
	if err := json.Unmarshal(data, &ps); err != nil {
		return []AuthProfile{}
	}
	return ps
}

func saveAuthProfiles(ps []AuthProfile) error {
	if ps == nil {
		ps = []AuthProfile{}
	}
	data, _ := json.MarshalIndent(ps, "", "  ")
	return os.WriteFile(authFile, data, 0644)
}

func addAuthProfile(p AuthProfile) AuthProfile {
	ps := loadAuthProfiles()
	p.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	p.CreatedAt = time.Now().Format("Jan 2, 3:04 PM")
	ps = append([]AuthProfile{p}, ps...)
	saveAuthProfiles(ps)
	return p
}

func deleteAuthProfile(id string) {
	ps := loadAuthProfiles()
	out := ps[:0]
	for _, p := range ps {
		if p.ID != id {
			out = append(out, p)
		}
	}
	saveAuthProfiles(out)
}

func getAuthProfile(id string) (AuthProfile, bool) {
	for _, p := range loadAuthProfiles() {
		if p.ID == id {
			return p, true
		}
	}
	return AuthProfile{}, false
}

func updateAuthProfile(updated AuthProfile) {
	ps := loadAuthProfiles()
	for i, p := range ps {
		if p.ID == updated.ID {
			ps[i] = updated
			break
		}
	}
	saveAuthProfiles(ps)
}

// ── Auth injection ─────────────────────────────────────────────────────────

// injectAuth calls the profile's login endpoint (if not cached / expired),
// then injects the token into req.
// Pass force=true to bypass the cache and fetch a fresh token (used on 401 retry).
func injectAuth(req *http.Request, profileID string, force bool) error {
	if profileID == "" {
		return nil
	}
	p, ok := getAuthProfile(profileID)
	if !ok {
		return nil // profile deleted — silently skip
	}

	token, err := getLoginToken(&p, force)
	if err != nil {
		return fmt.Errorf("Token API error: %w", err)
	}

	headerName := p.TokenHeaderName
	if headerName == "" {
		headerName = "Authorization"
	}
	prefix := p.TokenPrefix
	if prefix == "" && strings.EqualFold(headerName, "authorization") {
		prefix = "Bearer "
	}
	req.Header.Set(headerName, prefix+token)
	return nil
}

// ── Token API (login) flow ─────────────────────────────────────────────────

// getLoginToken calls the configured login endpoint, extracts the token from
// the JSON response at TokenPath (dot-notation), caches it, and returns it.
// Pass force=true to bypass the cache and always fetch a fresh token.
func getLoginToken(p *AuthProfile, force bool) (string, error) {
	// Return cached token if still valid (30-second buffer)
	if !force && p.AccessToken != "" && p.ExpiresAt > time.Now().Unix()+30 {
		return p.AccessToken, nil
	}

	loginURL := expandVariables(p.LoginURL)
	loginBody := expandVariables(p.LoginBody)

	if loginURL == "" {
		return "", fmt.Errorf("no login URL configured for this profile")
	}

	method := strings.ToUpper(p.LoginMethod)
	if method == "" {
		method = "POST"
	}

	var bodyReader io.Reader
	if loginBody != "" {
		bodyReader = strings.NewReader(loginBody)
	}

	req, err := http.NewRequest(method, loginURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("invalid login URL: %w", err)
	}

	switch p.LoginBodyType {
	case "json":
		req.Header.Set("Content-Type", "application/json")
	case "form":
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	for k, v := range parseLoginHeaders(p.LoginHeadersRaw) {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300] + "…"
		}
		return "", fmt.Errorf("login API returned %d: %s", resp.StatusCode, preview)
	}

	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("login response is not valid JSON (got: %s…)", string(body[:min(len(body), 80)]))
	}

	tokenPath := p.TokenPath
	if tokenPath == "" {
		tokenPath = "access_token"
	}
	token := extractJSONPath(parsed, tokenPath)
	if token == "" {
		return "", fmt.Errorf("could not find token at path %q in login response.\n\nResponse: %s", tokenPath, string(body[:min(len(body), 400)]))
	}

	p.AccessToken = token

	if p.ExpiryPath != "" {
		if val := extractJSONPath(parsed, p.ExpiryPath); val != "" {
			if secs, err := strconv.ParseInt(val, 10, 64); err == nil && secs > 0 {
				p.ExpiresAt = time.Now().Unix() + secs
			}
		}
	}
	if p.ExpiresAt <= time.Now().Unix() {
		p.ExpiresAt = time.Now().Unix() + 3600
	}

	updateAuthProfile(*p)
	return token, nil
}

// extractJSONPath traverses a parsed JSON value using a dot-notation path.
func extractJSONPath(v interface{}, path string) string {
	parts := strings.SplitN(path, ".", 2)
	key := parts[0]

	obj, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	val, exists := obj[key]
	if !exists {
		return ""
	}
	if len(parts) == 1 {
		switch t := val.(type) {
		case string:
			return t
		case float64:
			return strconv.FormatInt(int64(t), 10)
		case bool:
			return strconv.FormatBool(t)
		}
		return ""
	}
	return extractJSONPath(val, parts[1])
}

// parseLoginHeaders parses a "Key: Value\n..." string into a map.
func parseLoginHeaders(raw string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── HTTP Handlers ──────────────────────────────────────────────────────────

func authPanelData() gin.H {
	return gin.H{"Profiles": loadAuthProfiles()}
}

// AuthProfilesPanelHandler handles GET /auth-profiles
func AuthProfilesPanelHandler(c *gin.Context) {
	c.HTML(200, "auth_profiles.html", authPanelData())
}

// AuthProfileCreateHandler handles POST /auth-profiles
func AuthProfileCreateHandler(c *gin.Context) {
	p := AuthProfile{
		Type:            "login",
		Name:            c.PostForm("name"),
		LoginMethod:     c.PostForm("login_method"),
		LoginURL:        c.PostForm("login_url"),
		LoginBodyType:   c.PostForm("login_body_type"),
		LoginBody:       c.PostForm("login_body"),
		LoginHeadersRaw: c.PostForm("login_headers_raw"),
		TokenPath:       c.PostForm("token_path"),
		TokenHeaderName: c.PostForm("token_header_name"),
		TokenPrefix:     c.PostForm("token_prefix"),
		ExpiryPath:      c.PostForm("expiry_path"),
	}
	if p.Name == "" {
		p.Name = "Token API Profile"
	}
	addAuthProfile(p)
	c.Header("HX-Trigger", "authProfilesUpdated")
	c.HTML(200, "auth_profiles.html", authPanelData())
}

// AuthProfileDeleteHandler handles DELETE /auth-profiles/:id
func AuthProfileDeleteHandler(c *gin.Context) {
	deleteAuthProfile(c.Param("id"))
	c.Header("HX-Trigger", "authProfilesUpdated")
	c.HTML(200, "auth_profiles.html", authPanelData())
}

// AuthProfilesOptionsHandler handles GET /auth-profiles/options
func AuthProfilesOptionsHandler(c *gin.Context) {
	c.HTML(200, "auth_options.html", gin.H{"Profiles": loadAuthProfiles()})
}

// AuthProfileTestLoginHandler handles POST /auth-profiles/test-login
func AuthProfileTestLoginHandler(c *gin.Context) {
	p := AuthProfile{
		LoginMethod:     c.PostForm("login_method"),
		LoginURL:        c.PostForm("login_url"),
		LoginBodyType:   c.PostForm("login_body_type"),
		LoginBody:       c.PostForm("login_body"),
		LoginHeadersRaw: c.PostForm("login_headers_raw"),
		TokenPath:       c.PostForm("token_path"),
		TokenHeaderName: c.PostForm("token_header_name"),
		TokenPrefix:     c.PostForm("token_prefix"),
		ExpiryPath:      c.PostForm("expiry_path"),
	}

	token, err := getLoginToken(&p, false)
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "error": err.Error()})
		return
	}

	headerName := p.TokenHeaderName
	if headerName == "" {
		headerName = "Authorization"
	}
	prefix := p.TokenPrefix
	if prefix == "" && strings.EqualFold(headerName, "authorization") {
		prefix = "Bearer "
	}

	c.JSON(200, gin.H{
		"ok":         true,
		"token":      token,
		"inject_as":  headerName + ": " + prefix + token[:min(len(token), 40)] + "…",
		"expires_at": p.ExpiresAt,
	})
}
