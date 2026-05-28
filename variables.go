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
type Variable struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
}

// varRe matches every {{IDENTIFIER}} placeholder in a string.
// Valid names: start with a letter or underscore, followed by letters/digits/underscores.
var varRe = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// varReSingle matches {IDENTIFIER} (single-brace) as a forgiving fallback.
// Only used when the variable name is actually defined in the map.
var varReSingle = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ── Persistence (global variables) ────────────────────────────────────────

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

func saveVariables(vs []Variable) error {
	if vs == nil {
		vs = []Variable{}
	}
	data, _ := json.MarshalIndent(vs, "", "  ")
	return os.WriteFile(varsFile, data, 0644)
}

// upsertVariable saves a global variable: updates value in-place if the name
// already exists, otherwise prepends a new entry.
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
// the given collection context. Supports both {{NAME}} (canonical) and {NAME}
// (single-brace convenience) — single-brace is only expanded when the variable
// IS defined, so literal {braces} in bodies are left untouched.
// Unresolved placeholders are left unchanged.
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

// expandVariables is the no-collection-context variant (global + active env only).
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

// VariablesPanelHandler handles GET /variables
func VariablesPanelHandler(c *gin.Context) {
	c.HTML(200, "variables.html", buildVarsData())
}

// VariableCreateHandler handles POST /variables — creates/updates a global variable.
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

// VariableDeleteHandler handles DELETE /variables/:id — deletes a global variable.
func VariableDeleteHandler(c *gin.Context) {
	deleteVariable(c.Param("id"))
	c.Header("HX-Trigger", "variablesUpdated")
	c.HTML(200, "variables.html", buildVarsData())
}

// VariablePatchHandler handles PATCH /variables/:id — updates a global variable's value inline.
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

// VariablesMapHandler handles GET /variables/map
// Returns the fully-merged variable set as a flat JSON object for the
// client-side URL-bar live preview. Accepts an optional ?collection_id= query
// param so the preview also reflects collection-scoped variables.
func VariablesMapHandler(c *gin.Context) {
	c.JSON(200, buildVarMap(c.Query("collection_id")))
}

// VariablesListHandler handles GET /variables/list
// Returns the raw global variable list as JSON (includes IDs needed for deletion).
func VariablesListHandler(c *gin.Context) {
	c.JSON(200, loadVariables())
}
