package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const authFile = "auth_profiles.json"

// AuthProfile is a saved authentication configuration for a specific API.
type AuthProfile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"` // bearer | basic | apikey | oauth2 | login

	// Bearer Token
	Token string `json:"token,omitempty"`

	// Basic Auth
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// API Key
	KeyName  string `json:"key_name,omitempty"`
	KeyValue string `json:"key_value,omitempty"`
	KeyIn    string `json:"key_in,omitempty"` // header | query

	// OAuth2 (client_credentials grant)
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	Scope        string `json:"scope,omitempty"`

	// Login / Token API — custom auth endpoint
	// Makes a configurable HTTP call, extracts token from JSON response,
	// injects it into subsequent requests.
	LoginMethod     string `json:"login_method,omitempty"`      // GET | POST (default POST)
	LoginURL        string `json:"login_url,omitempty"`         // e.g. https://api.example.com/auth/login
	LoginBodyType   string `json:"login_body_type,omitempty"`   // json | form (sets Content-Type)
	LoginBody       string `json:"login_body,omitempty"`        // request body / credentials
	LoginHeadersRaw string `json:"login_headers_raw,omitempty"` // "Key: Value\n..." extra headers

	// Where to find the token in the JSON response (dot-notation path)
	// e.g.  "access_token"  or  "data.token"  or  "result.auth.jwt"
	TokenPath string `json:"token_path,omitempty"`

	// How to inject the extracted token into downstream requests
	TokenHeaderName string `json:"token_header_name,omitempty"` // default: Authorization
	TokenPrefix     string `json:"token_prefix,omitempty"`      // default: "Bearer "

	// Optional: path to an integer seconds-until-expiry field in the response
	// e.g. "expires_in"  (if omitted, token is cached for 1 hour)
	ExpiryPath string `json:"expiry_path,omitempty"`

	// Shared token cache (used by oauth2 and login types)
	AccessToken string `json:"access_token,omitempty"` // cached token
	ExpiresAt   int64  `json:"expires_at,omitempty"`   // Unix timestamp

	CreatedAt string `json:"created_at"`
}

// TypeLabel returns a human-readable label for the profile type.
func (p AuthProfile) TypeLabel() string {
	switch p.Type {
	case "bearer":
		return "Bearer Token"
	case "basic":
		return "Basic Auth"
	case "apikey":
		return "API Key"
	case "oauth2":
		return "OAuth2"
	case "login":
		return "Token API"
	}
	return p.Type
}

// TypeBadgeClass returns a DaisyUI badge variant for the type.
func (p AuthProfile) TypeBadgeClass() string {
	switch p.Type {
	case "bearer":
		return "badge-primary"
	case "basic":
		return "badge-secondary"
	case "apikey":
		return "badge-accent"
	case "oauth2":
		return "badge-info"
	case "login":
		return "badge-success"
	}
	return "badge-ghost"
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

// addAuthProfile prepends a new profile and returns it with its generated ID.
func addAuthProfile(p AuthProfile) AuthProfile {
	ps := loadAuthProfiles()
	p.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	p.CreatedAt = time.Now().Format("Jan 2, 3:04 PM")
	ps = append([]AuthProfile{p}, ps...)
	saveAuthProfiles(ps)
	return p
}

// deleteAuthProfile removes a profile by ID.
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

// getAuthProfile looks up a profile by ID.
func getAuthProfile(id string) (AuthProfile, bool) {
	for _, p := range loadAuthProfiles() {
		if p.ID == id {
			return p, true
		}
	}
	return AuthProfile{}, false
}

// updateAuthProfile replaces a profile in place (used to cache OAuth2 tokens).
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

// injectAuth modifies req to include auth from the named profile.
// For OAuth2 it may perform a network call to fetch/refresh the access token.
func injectAuth(req *http.Request, profileID string) error {
	if profileID == "" {
		return nil
	}
	p, ok := getAuthProfile(profileID)
	if !ok {
		return nil // profile was deleted — silently skip
	}
	switch p.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+p.Token)

	case "basic":
		enc := base64.StdEncoding.EncodeToString(
			[]byte(p.Username + ":" + p.Password))
		req.Header.Set("Authorization", "Basic "+enc)

	case "apikey":
		if p.KeyIn == "query" {
			q := req.URL.Query()
			q.Set(p.KeyName, p.KeyValue)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set(p.KeyName, p.KeyValue)
		}

	case "oauth2":
		token, err := getOAuth2Token(&p)
		if err != nil {
			return fmt.Errorf("OAuth2 token error: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

	case "login":
		token, err := getLoginToken(&p)
		if err != nil {
			return fmt.Errorf("Token API error: %w", err)
		}
		headerName := p.TokenHeaderName
		if headerName == "" {
			headerName = "Authorization"
		}
		prefix := p.TokenPrefix
		// Default to "Bearer " when injecting into Authorization header
		if prefix == "" && strings.EqualFold(headerName, "authorization") {
			prefix = "Bearer "
		}
		req.Header.Set(headerName, prefix+token)
	}
	return nil
}

// getOAuth2Token returns a valid access token, fetching a new one if expired.
func getOAuth2Token(p *AuthProfile) (string, error) {
	// Return cached token if still valid (with 30s buffer)
	if p.AccessToken != "" && p.ExpiresAt > time.Now().Unix()+30 {
		return p.AccessToken, nil
	}

	// Fetch via client_credentials grant
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", p.ClientID)
	form.Set("client_secret", p.ClientSecret)
	if p.Scope != "" {
		form.Set("scope", p.Scope)
	}

	resp, err := http.PostForm(p.TokenURL, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(b, &tok); err != nil {
		return "", fmt.Errorf("invalid token response")
	}
	if tok.Error != "" {
		return "", fmt.Errorf("%s: %s", tok.Error, tok.ErrorDesc)
	}

	// Cache the new token back to disk
	p.AccessToken = tok.AccessToken
	if tok.ExpiresIn > 0 {
		p.ExpiresAt = time.Now().Unix() + tok.ExpiresIn
	} else {
		p.ExpiresAt = time.Now().Unix() + 3600
	}
	updateAuthProfile(*p)

	return p.AccessToken, nil
}

// suggestAuthType analyzes a 401 response and returns the most likely auth type.
// ── Login / Token API ──────────────────────────────────────────────────────

// getLoginToken calls the configured login endpoint, extracts the token from
// the JSON response at TokenPath (dot-notation), caches it, and returns it.
// If the cached token is still valid it is returned immediately without a call.
func getLoginToken(p *AuthProfile) (string, error) {
	// Return cached token if still valid (30-second buffer)
	if p.AccessToken != "" && p.ExpiresAt > time.Now().Unix()+30 {
		return p.AccessToken, nil
	}

	if p.LoginURL == "" {
		return "", fmt.Errorf("no login URL configured for this profile")
	}

	method := strings.ToUpper(p.LoginMethod)
	if method == "" {
		method = "POST"
	}

	// Build request body
	var bodyReader io.Reader
	if p.LoginBody != "" {
		bodyReader = strings.NewReader(p.LoginBody)
	}

	req, err := http.NewRequest(method, p.LoginURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("invalid login URL: %w", err)
	}

	// Set Content-Type based on body type selection
	switch p.LoginBodyType {
	case "json":
		req.Header.Set("Content-Type", "application/json")
	case "form":
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// Apply any extra headers the user configured ("Key: Value" per line)
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

	// Parse the JSON response
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("login response is not valid JSON (got: %s…)", string(body[:min(len(body), 80)]))
	}

	// Extract token using dot-notation path
	tokenPath := p.TokenPath
	if tokenPath == "" {
		tokenPath = "access_token" // sensible default
	}
	token := extractJSONPath(parsed, tokenPath)
	if token == "" {
		return "", fmt.Errorf("could not find token at path %q in login response.\n\nResponse: %s", tokenPath, string(body[:min(len(body), 400)]))
	}

	// Cache the token
	p.AccessToken = token

	// Try to read expiry from the response
	if p.ExpiryPath != "" {
		if val := extractJSONPath(parsed, p.ExpiryPath); val != "" {
			if secs, err := strconv.ParseInt(val, 10, 64); err == nil && secs > 0 {
				p.ExpiresAt = time.Now().Unix() + secs
			}
		}
	}
	if p.ExpiresAt <= time.Now().Unix() {
		p.ExpiresAt = time.Now().Unix() + 3600 // default: cache for 1 hour
	}

	updateAuthProfile(*p)
	return token, nil
}

// extractJSONPath traverses a parsed JSON value using a dot-notation path.
// e.g. "data.token"  →  {"data":{"token":"eyJ..."}}  →  "eyJ..."
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
		// Leaf — convert to string
		switch t := val.(type) {
		case string:
			return t
		case float64:
			// integers come back as float64 from JSON
			return strconv.FormatInt(int64(t), 10)
		case bool:
			return strconv.FormatBool(t)
		}
		return ""
	}
	// Recurse into nested object
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

// min returns the smaller of a and b (Go 1.21+ has this as a builtin;
// kept here for clarity and older toolchain safety).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func suggestAuthType(respHeaders map[string]string, respBody string) string {
	for k, v := range respHeaders {
		if strings.EqualFold(k, "www-authenticate") {
			lower := strings.ToLower(v)
			if strings.HasPrefix(lower, "bearer") {
				return "bearer"
			}
			if strings.HasPrefix(lower, "basic") {
				return "basic"
			}
		}
	}
	lower := strings.ToLower(respBody)
	switch {
	case strings.Contains(lower, "oauth"):
		return "oauth2"
	case strings.Contains(lower, "api_key"),
		strings.Contains(lower, "api-key"),
		strings.Contains(lower, "apikey"):
		return "apikey"
	}
	return "bearer" // sensible default
}

// ── HTTP Handlers ──────────────────────────────────────────────────────────

// AuthProfilesPanelHandler handles GET /auth-profiles
// Returns the modal inner HTML for the auth profiles manager.
func AuthProfilesPanelHandler(c *gin.Context) {
	c.HTML(200, "auth_profiles.html", gin.H{
		"Profiles": loadAuthProfiles(),
	})
}

// AuthProfileCreateHandler handles POST /auth-profiles
// Creates a new profile and returns the updated panel HTML.
func AuthProfileCreateHandler(c *gin.Context) {
	p := AuthProfile{
		// Common
		Name: c.PostForm("name"),
		Type: c.PostForm("type"),
		// Bearer
		Token: c.PostForm("token"),
		// Basic
		Username: c.PostForm("username"),
		Password: c.PostForm("password"),
		// API Key
		KeyName:  c.PostForm("key_name"),
		KeyValue: c.PostForm("key_value"),
		KeyIn:    c.PostForm("key_in"),
		// OAuth2
		ClientID:     c.PostForm("client_id"),
		ClientSecret: c.PostForm("client_secret"),
		TokenURL:     c.PostForm("token_url"),
		Scope:        c.PostForm("scope"),
		// Token API (login)
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
		p.Name = p.TypeLabel() + " Profile"
	}
	addAuthProfile(p)

	// Notify the auth select dropdown to refresh
	c.Header("HX-Trigger", "authProfilesUpdated")
	c.HTML(200, "auth_profiles.html", gin.H{
		"Profiles": loadAuthProfiles(),
	})
}

// AuthProfileDeleteHandler handles DELETE /auth-profiles/:id
func AuthProfileDeleteHandler(c *gin.Context) {
	deleteAuthProfile(c.Param("id"))
	c.Header("HX-Trigger", "authProfilesUpdated")
	c.HTML(200, "auth_profiles.html", gin.H{
		"Profiles": loadAuthProfiles(),
	})
}

// AuthProfilesOptionsHandler handles GET /auth-profiles/options
// Returns only the <option> elements for the auth profile select dropdown.
func AuthProfilesOptionsHandler(c *gin.Context) {
	c.HTML(200, "auth_options.html", gin.H{
		"Profiles": loadAuthProfiles(),
	})
}

// AuthProfileTestLoginHandler handles POST /auth-profiles/test-login
// Performs a dry-run of the login API call and returns the result as JSON
// so the user can verify their config before saving the profile.
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

	token, err := getLoginToken(&p)
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
		"ok":           true,
		"token":        token,
		"inject_as":    headerName + ": " + prefix + token[:min(len(token), 40)] + "…",
		"expires_at":   p.ExpiresAt,
	})
}
