package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

const historyFile = "history.json"
const maxHistory = 100

// HistoryEntry is one saved request + its result summary.
type HistoryEntry struct {
	ID            string   `json:"id"`
	Method        string   `json:"method"`
	URL           string   `json:"url"`
	Headers       []KVPair `json:"headers"`
	Params        []KVPair `json:"params"`
	Body          string   `json:"body"`
	BodyType      string   `json:"body_type,omitempty"`      // json | xml | form | text
	AuthProfileID string   `json:"auth_profile_id,omitempty"` // linked auth profile
	Status        int      `json:"status"`
	Duration      string   `json:"duration"`
	CreatedAt     string   `json:"created_at"`
}

// StatusClass returns the DaisyUI badge variant for the HTTP status code.
func (e HistoryEntry) StatusClass() string {
	switch {
	case e.Status >= 200 && e.Status < 300:
		return "badge-success"
	case e.Status >= 300 && e.Status < 400:
		return "badge-warning"
	case e.Status >= 400:
		return "badge-error"
	default:
		return "badge-ghost"
	}
}

// ShortURL returns the URL truncated to 40 chars for sidebar display.
func (e HistoryEntry) ShortURL() string {
	if len(e.URL) > 40 {
		return e.URL[:40] + "…"
	}
	return e.URL
}

// ── Persistence ────────────────────────────────────────────────────────────

func loadHistory() []HistoryEntry {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		return []HistoryEntry{}
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return []HistoryEntry{}
	}
	return entries
}

func saveHistory(entries []HistoryEntry) error {
	if entries == nil {
		entries = []HistoryEntry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(historyFile, data, 0644)
}

// addToHistory prepends a new entry (newest first) and trims to maxHistory.
func addToHistory(entry HistoryEntry) {
	entries := loadHistory()
	entries = append([]HistoryEntry{entry}, entries...)
	if len(entries) > maxHistory {
		entries = entries[:maxHistory]
	}
	saveHistory(entries)
}

// deleteHistoryEntry removes a single entry by ID.
func deleteHistoryEntry(id string) {
	entries := loadHistory()
	out := entries[:0]
	for _, e := range entries {
		if e.ID != id {
			out = append(out, e)
		}
	}
	saveHistory(out)
}

// newHistoryEntry builds a HistoryEntry from request inputs and response outcome.
func newHistoryEntry(
	method, rawURL string,
	headers, params []KVPair,
	body, bodyType, authProfileID string,
	status int,
	duration string,
) HistoryEntry {
	return HistoryEntry{
		ID:            fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:        method,
		URL:           rawURL,
		Headers:       headers,
		Params:        params,
		Body:          body,
		BodyType:      bodyType,
		AuthProfileID: authProfileID,
		Status:        status,
		Duration:      duration,
		CreatedAt:     time.Now().Format("Jan 2, 3:04 PM"),
	}
}

// ── Handlers ───────────────────────────────────────────────────────────────

// HistoryPanelHandler handles GET /history-panel
// Returns the history list HTML — HTMX drops it into the sidebar.
func HistoryPanelHandler(c *gin.Context) {
	entries := loadHistory()
	c.HTML(200, "history.html", gin.H{
		"Entries": entries,
	})
}

// HistoryLoadHandler handles GET /history/:id
// Finds the entry and returns the pre-filled form HTML.
func HistoryLoadHandler(c *gin.Context) {
	id := c.Param("id")
	for _, entry := range loadHistory() {
		if entry.ID == id {
			headers := entry.Headers
			for len(headers) < 2 {
				headers = append(headers, KVPair{"", ""})
			}
			params := entry.Params
			if len(params) == 0 {
				params = append(params, KVPair{"", ""})
			}
			bodyType := entry.BodyType
			if bodyType == "" {
				bodyType = "json"
			}
			c.HTML(200, "form.html", gin.H{
				"Method":        entry.Method,
				"URL":           entry.URL,
				"Headers":       headers,
				"Params":        params,
				"Body":          entry.Body,
				"BodyType":      bodyType,
				"AuthProfileID": entry.AuthProfileID,
			})
			return
		}
	}
	// ID not found — return blank form
	c.HTML(200, "form.html", defaultFormData())
}

// HistoryDeleteHandler handles DELETE /history/:id
// Deletes one entry and fires historyUpdated so the panel reloads.
func HistoryDeleteHandler(c *gin.Context) {
	deleteHistoryEntry(c.Param("id"))
	c.Header("HX-Trigger", "historyUpdated")
	c.Status(http.StatusNoContent)
}

// HistoryExportHandler handles GET /history/export
// Serves history.json as a file download.
func HistoryExportHandler(c *gin.Context) {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		data = []byte("[]")
	}
	c.Header("Content-Disposition", "attachment; filename=api-playground-history.json")
	c.Data(200, "application/json", data)
}

// HistoryImportHandler handles POST /history/import
// Merges an uploaded JSON file into the existing history (deduplicates by ID).
func HistoryImportHandler(c *gin.Context) {
	file, err := c.FormFile("import_file")
	if err != nil {
		c.Header("HX-Trigger", "historyUpdated")
		c.Status(http.StatusBadRequest)
		return
	}

	f, err := file.Open()
	if err != nil {
		c.Header("HX-Trigger", "historyUpdated")
		c.Status(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	var imported []HistoryEntry
	if err := json.NewDecoder(f).Decode(&imported); err != nil {
		c.Header("HX-Trigger", "historyUpdated")
		c.Status(http.StatusBadRequest)
		return
	}

	// Merge: deduplicate by ID, put imported entries first
	existing := loadHistory()
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[e.ID] = true
	}
	var merged []HistoryEntry
	for _, e := range imported {
		if !seen[e.ID] {
			merged = append(merged, e)
			seen[e.ID] = true
		}
	}
	merged = append(merged, existing...)
	if len(merged) > maxHistory {
		merged = merged[:maxHistory]
	}
	saveHistory(merged)

	c.Header("HX-Trigger", "historyUpdated")
	c.Status(http.StatusNoContent)
}
