package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	envsFile     = "environments.json"
	settingsFile = "settings.json"
)

// Environment groups variables under a named deployment context
// (e.g. "Development", "Staging", "Production").
// Auth profiles reference environments via their EnvID field.
type Environment struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Variables []EnvVar `json:"variables"`
	CreatedAt string   `json:"created_at"`
}

// EnvVar is a key/value variable scoped to a specific environment.
type EnvVar struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AppSettings holds lightweight persistent app state.
type AppSettings struct {
	ActiveEnvID string `json:"active_env_id"`
}

// ── Settings (active environment) ─────────────────────────────────────────

func loadSettings() AppSettings {
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		return AppSettings{}
	}
	var s AppSettings
	json.Unmarshal(data, &s)
	return s
}

func saveSettings(s AppSettings) {
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(settingsFile, data, 0644)
}

func getActiveEnvID() string { return loadSettings().ActiveEnvID }

// getActiveEnv returns a pointer to the active Environment, or nil when none
// is set or the previously-active environment has since been deleted.
func getActiveEnv() *Environment {
	id := getActiveEnvID()
	if id == "" {
		return nil
	}
	for _, e := range loadEnvironments() {
		if e.ID == id {
			cp := e
			return &cp
		}
	}
	return nil // was deleted — treat as no active env
}

// ── Environment persistence ────────────────────────────────────────────────

func loadEnvironments() []Environment {
	data, err := os.ReadFile(envsFile)
	if err != nil {
		return []Environment{}
	}
	var es []Environment
	if err := json.Unmarshal(data, &es); err != nil {
		return []Environment{}
	}
	return es
}

func saveEnvironments(es []Environment) {
	if es == nil {
		es = []Environment{}
	}
	data, _ := json.MarshalIndent(es, "", "  ")
	os.WriteFile(envsFile, data, 0644)
}

func createEnvironment(name string) Environment {
	es := loadEnvironments()
	e := Environment{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Name:      name,
		Variables: []EnvVar{},
		CreatedAt: time.Now().Format("Jan 2, 3:04 PM"),
	}
	es = append([]Environment{e}, es...)
	saveEnvironments(es)
	return e
}

func deleteEnvironment(id string) {
	es := loadEnvironments()
	out := es[:0]
	for _, e := range es {
		if e.ID != id {
			out = append(out, e)
		}
	}
	saveEnvironments(out)
	// If the deleted environment was active, clear the setting
	if getActiveEnvID() == id {
		saveSettings(AppSettings{})
	}
}

// ── Environment variable CRUD ──────────────────────────────────────────────

// upsertEnvVar adds a variable to env envID, or updates its value if a
// variable with the same name already exists in that environment.
func upsertEnvVar(envID string, incoming EnvVar) {
	es := loadEnvironments()
	for i, e := range es {
		if e.ID != envID {
			continue
		}
		for j, v := range e.Variables {
			if v.Name == incoming.Name {
				es[i].Variables[j].Value = incoming.Value
				saveEnvironments(es)
				return
			}
		}
		incoming.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		es[i].Variables = append([]EnvVar{incoming}, es[i].Variables...)
		saveEnvironments(es)
		return
	}
}

func deleteEnvVar(envID, varID string) {
	es := loadEnvironments()
	for i, e := range es {
		if e.ID != envID {
			continue
		}
		out := e.Variables[:0]
		for _, v := range e.Variables {
			if v.ID != varID {
				out = append(out, v)
			}
		}
		es[i].Variables = out
		saveEnvironments(es)
		return
	}
}

// ── Shared template-data helper ────────────────────────────────────────────

// envBaseData returns the common environment fields used by multiple templates.
func envBaseData() gin.H {
	return gin.H{
		"Environments": loadEnvironments(),
		"ActiveEnv":    getActiveEnv(),
		"ActiveEnvID":  getActiveEnvID(),
	}
}

// ── HTTP Handlers ──────────────────────────────────────────────────────────

// EnvSelectorHandler handles GET /env-selector
// Returns the navbar environment picker widget (loaded by HTMX).
func EnvSelectorHandler(c *gin.Context) {
	c.HTML(200, "env_selector.html", envBaseData())
}

// EnvironmentsPanelHandler handles GET /environments
func EnvironmentsPanelHandler(c *gin.Context) {
	c.HTML(200, "environments.html", envBaseData())
}

// EnvironmentCreateHandler handles POST /environments
func EnvironmentCreateHandler(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		name = "New Environment"
	}
	createEnvironment(name)
	c.Header("HX-Trigger", "environmentsUpdated")
	c.Status(200)
}

// EnvironmentDeleteHandler handles DELETE /environments/:id
func EnvironmentDeleteHandler(c *gin.Context) {
	deleteEnvironment(c.Param("id"))
	c.Header("HX-Trigger", "environmentsUpdated")
	c.Status(200)
}

// EnvironmentActivateHandler handles POST /environments/:id/activate
func EnvironmentActivateHandler(c *gin.Context) {
	saveSettings(AppSettings{ActiveEnvID: c.Param("id")})
	c.Header("HX-Trigger", "environmentsUpdated")
	c.Status(200)
}

// EnvironmentDeactivateHandler handles POST /environments/deactivate
func EnvironmentDeactivateHandler(c *gin.Context) {
	saveSettings(AppSettings{})
	c.Header("HX-Trigger", "environmentsUpdated")
	c.Status(200)
}

// EnvVariableCreateHandler handles POST /environments/:id/variables
func EnvVariableCreateHandler(c *gin.Context) {
	envID := c.Param("id")
	name := strings.TrimSpace(c.PostForm("name"))
	data := buildVarsData()
	if name == "" || !varRe.MatchString("{{"+name+"}}") {
		data["Error"] = "Invalid name — letters, digits, and underscores only; cannot start with a digit."
		c.HTML(200, "variables.html", data)
		return
	}
	upsertEnvVar(envID, EnvVar{Name: name, Value: c.PostForm("value")})
	c.Header("HX-Trigger", "variablesUpdated")
	c.HTML(200, "variables.html", buildVarsData())
}

// EnvVariableDeleteHandler handles DELETE /environments/:id/variables/:var_id
func EnvVariableDeleteHandler(c *gin.Context) {
	deleteEnvVar(c.Param("id"), c.Param("var_id"))
	c.Header("HX-Trigger", "variablesUpdated")
	c.HTML(200, "variables.html", buildVarsData())
}
