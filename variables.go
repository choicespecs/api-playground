package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const varsFile = "variables.json"

// Variable is a global key/value substitution value referenced as {{NAME}}.
// Environment-scoped variables live inside Environment.Variables instead.
// Variables are persisted to varsFile and loaded fresh on every read.
// IDs are nanosecond Unix timestamps formatted as decimal strings.
type Variable struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
}

// varRe matches every {{IDENTIFIER}} placeholder in a string.
// Valid names: start with a letter or underscore, followed by letters/digits/underscores.
// Used for both expansion and name validation.
var varRe = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// varReSingle matches {IDENTIFIER} (single-brace) as a forgiving fallback.
// Only used when the variable name is actually defined in the map, so literal
// JSON braces like {"key": "value"} are never incorrectly expanded.
var varReSingle = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ── Persistence (global variables) ────────────────────────────────────────

// loadVariables reads all global variables from varsFile.
// Returns an empty slice on any error (file not found, parse failure).
func loadVariables() []Variable {
	data, err := os.ReadFile(varsFile)
	if err != nil {
		return []Variable{}
	}
	var vs []Variable
	if err := json.Unmarshal(data, &vs); err != nil {
		return []Variable{}
	}
	return vs
}

// saveVariables writes vs to varsFile with 2-space indented JSON.
// A nil slice is written as an empty JSON array.
func saveVariables(vs []Variable) error {
	if vs == nil {
		vs = []Variable{}
	}
	data, _ := json.MarshalIndent(vs, "", "  ")
	return os.WriteFile(varsFile, data, 0644)
}

// upsertVariable saves a global variable: updates the value in-place if a
// variable with the same name already exists, otherwise prepends a new entry
// (newest first ordering). Returns the saved variable (with ID and CreatedAt
// set for new entries).
func upsertVariable(incoming Variable) Variable {
	vs := loadVariables()
	for i, v := range vs {
		if v.Name == incoming.Name {
			vs[i].Value = incoming.Value
			saveVariables(vs)
			return vs[i]
		}
	}
	incoming.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	incoming.CreatedAt = time.Now().Format("Jan 2, 3:04 PM")
	vs = append([]Variable{incoming}, vs...)
	saveVariables(vs)
	return incoming
}

// deleteVariable removes the variable with the given ID from varsFile.
// It is a no-op if the ID is not found.
func deleteVariable(id string) {
	vs := loadVariables()
	out := vs[:0]
	for _, v := range vs {
		if v.ID != id {
			out = append(out, v)
		}
	}
	saveVariables(out)
}

// ── Expansion ──────────────────────────────────────────────────────────────

// buildVarMap constructs the merged variable map for a given collection context.
// Resolution order (later entries win): global → collection.
// Returns a flat map[name]value suitable for string substitution.
func buildVarMap(collID string) map[string]string {
	m := make(map[string]string)
	for _, v := range loadVariables() {
		m[v.Name] = v.Value
	}
	if collID != "" {
		if coll, ok := getCollection(collID); ok {
			for _, v := range coll.Variables {
				m[v.Name] = v.Value // collection overrides global
			}
		}
	}
	return m
}

// expandVariablesCtx replaces variable placeholders using the merged map for
// the given collection context. Two syntaxes are supported:
//   - {{NAME}} (canonical double-brace) — always expanded
//   - {NAME}  (single-brace convenience) — expanded only when NAME is in the map
//
// Single-brace expansion is conditional so that JSON bodies containing literal
// {braces} are not accidentally corrupted. Unresolved placeholders are left
// unchanged, so the caller (SendRequest) can detect them and report a clear error.
func expandVariablesCtx(s, collID string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	m := buildVarMap(collID)

	// Pass 1 — canonical {{NAME}} double-brace
	result := varRe.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-2]
		if val, ok := m[name]; ok {
			return val
		}
		return match
	})

	// Pass 2 — forgiving {NAME} single-brace (only when name IS in the map)
	if strings.Contains(result, "{") {
		result = varReSingle.ReplaceAllStringFunc(result, func(match string) string {
			name := match[1 : len(match)-1]
			if val, ok := m[name]; ok {
				return val
			}
			return match
		})
	}
	return result
}

// expandVariables is the no-collection-context variant (global variables only).
// Used by auth.go when expanding the login URL and body, which have no collection context.
func expandVariables(s string) string {
	return expandVariablesCtx(s, "")
}

// ── Template data helper ───────────────────────────────────────────────────

// buildVarsData returns the template context for the variables panel.
func buildVarsData() gin.H {
	return gin.H{
		"GlobalVars": loadVariables(),
	}
}

// ── HTTP Handlers ──────────────────────────────────────────────────────────

// VariablesPanelHandler handles GET /variables.
// Returns the variables modal HTML rendered from variables.html.
func VariablesPanelHandler(c *gin.Context) {
	c.HTML(200, "variables.html", buildVarsData())
}

// VariableCreateHandler handles POST /variables.
// Creates a new global variable or updates the value of an existing one with
// the same name (upsert semantics). Validates the name against varRe.
// Fires the variablesUpdated HX-Trigger to refresh dependent UI panels.
func VariableCreateHandler(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	data := buildVarsData()
	if name == "" || !varRe.MatchString("{{"+name+"}}") {
		data["Error"] = "Invalid name — letters, digits, and underscores only; cannot start with a digit."
		c.HTML(200, "variables.html", data)
		return
	}
	upsertVariable(Variable{
		Name:  name,
		Value: c.PostForm("value"),
	})
	c.Header("HX-Trigger", "variablesUpdated")
	c.HTML(200, "variables.html", buildVarsData())
}

// VariableDeleteHandler handles DELETE /variables/:id.
// Deletes the global variable with the given ID and fires variablesUpdated.
func VariableDeleteHandler(c *gin.Context) {
	deleteVariable(c.Param("id"))
	c.Header("HX-Trigger", "variablesUpdated")
	c.HTML(200, "variables.html", buildVarsData())
}

// VariablePatchHandler handles PATCH /variables/:id.
// Updates only the value of the variable with the given ID (inline edit).
// Fires variablesUpdated to sync the right panel and autocomplete map.
func VariablePatchHandler(c *gin.Context) {
	id := c.Param("id")
	vs := loadVariables()
	for i, v := range vs {
		if v.ID == id {
			vs[i].Value = c.PostForm("value")
			saveVariables(vs)
			break
		}
	}
	c.Header("HX-Trigger", "variablesUpdated")
	c.HTML(200, "variables.html", buildVarsData())
}

// VariablesMapHandler handles GET /variables/map.
// Returns the fully-merged variable set as a flat JSON object for the
// client-side URL-bar live preview. Accepts an optional ?collection_id= query
// param so the preview also reflects collection-scoped variables.
// Response: {"NAME": "value", ...}
func VariablesMapHandler(c *gin.Context) {
	c.JSON(200, buildVarMap(c.Query("collection_id")))
}

// VariablesListHandler handles GET /variables/list.
// Returns the raw global variable list as JSON (includes IDs needed for
// deletion and inline editing from the right panel).
// Response: [{"id": "...", "name": "...", "value": "...", "created_at": "..."}]
func VariablesListHandler(c *gin.Context) {
	c.JSON(200, loadVariables())
}
