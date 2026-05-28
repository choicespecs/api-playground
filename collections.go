package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const collectionsFile = "collections.json"

// Collection is a named folder that groups saved API requests together.
// It carries a default auth profile and its own set of variables that are
// layered on top of global and environment variables during expansion.
type Collection struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	AuthProfileID string         `json:"auth_profile_id,omitempty"` // default auth for requests in this collection
	Variables     []CollVar      `json:"variables"`
	Requests      []SavedRequest `json:"requests"`
	CreatedAt     string         `json:"created_at"`
}

// CollVar is a key/value variable scoped to a specific collection.
type CollVar struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SavedRequest is a snapshot of the request form saved inside a Collection.
type SavedRequest struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Method        string   `json:"method"`
	URL           string   `json:"url"`
	Headers       []KVPair `json:"headers"`
	Params        []KVPair `json:"params"`
	Body          string   `json:"body"`
	BodyType      string   `json:"body_type"`
	AuthProfileID string   `json:"auth_profile_id,omitempty"` // "" = inherit collection default
	CreatedAt     string   `json:"created_at"`
}

// ── Persistence ────────────────────────────────────────────────────────────

func loadCollections() []Collection {
	data, err := os.ReadFile(collectionsFile)
	if err != nil {
		return []Collection{}
	}
	var cs []Collection
	if err := json.Unmarshal(data, &cs); err != nil {
		return []Collection{}
	}
	return cs
}

func saveCollections(cs []Collection) {
	if cs == nil {
		cs = []Collection{}
	}
	data, _ := json.MarshalIndent(cs, "", "  ")
	os.WriteFile(collectionsFile, data, 0644)
}

func getCollection(id string) (Collection, bool) {
	for _, c := range loadCollections() {
		if c.ID == id {
			return c, true
		}
	}
	return Collection{}, false
}

func createCollection(name string) Collection {
	cs := loadCollections()
	c := Collection{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Name:      name,
		Variables: []CollVar{},
		Requests:  []SavedRequest{},
		CreatedAt: time.Now().Format("Jan 2, 3:04 PM"),
	}
	cs = append([]Collection{c}, cs...)
	saveCollections(cs)
	return c
}

func deleteCollection(id string) {
	cs := loadCollections()
	out := cs[:0]
	for _, c := range cs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	saveCollections(out)
}

func updateCollectionMeta(id, name, authProfileID string) {
	cs := loadCollections()
	for i, c := range cs {
		if c.ID == id {
			if name != "" {
				cs[i].Name = name
			}
			cs[i].AuthProfileID = authProfileID
			saveCollections(cs)
			return
		}
	}
}

// ── Saved-request CRUD ─────────────────────────────────────────────────────

func saveRequestToCollection(collID string, r SavedRequest) (SavedRequest, bool) {
	cs := loadCollections()
	for i, c := range cs {
		if c.ID != collID {
			continue
		}
		r.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		r.CreatedAt = time.Now().Format("Jan 2, 3:04 PM")
		cs[i].Requests = append([]SavedRequest{r}, cs[i].Requests...)
		saveCollections(cs)
		return r, true
	}
	return SavedRequest{}, false
}

func deleteRequestFromCollection(collID, reqID string) {
	cs := loadCollections()
	for i, c := range cs {
		if c.ID != collID {
			continue
		}
		out := c.Requests[:0]
		for _, r := range c.Requests {
			if r.ID != reqID {
				out = append(out, r)
			}
		}
		cs[i].Requests = out
		saveCollections(cs)
		return
	}
}

func getRequestFromCollection(collID, reqID string) (SavedRequest, Collection, bool) {
	c, ok := getCollection(collID)
	if !ok {
		return SavedRequest{}, Collection{}, false
	}
	for _, r := range c.Requests {
		if r.ID == reqID {
			return r, c, true
		}
	}
	return SavedRequest{}, Collection{}, false
}

// ── Collection variable CRUD ───────────────────────────────────────────────

func upsertCollVar(collID string, incoming CollVar) {
	cs := loadCollections()
	for i, c := range cs {
		if c.ID != collID {
			continue
		}
		for j, v := range c.Variables {
			if v.Name == incoming.Name {
				cs[i].Variables[j].Value = incoming.Value
				saveCollections(cs)
				return
			}
		}
		incoming.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		cs[i].Variables = append([]CollVar{incoming}, cs[i].Variables...)
		saveCollections(cs)
		return
	}
}

func deleteCollVar(collID, varID string) {
	cs := loadCollections()
	for i, c := range cs {
		if c.ID != collID {
			continue
		}
		out := c.Variables[:0]
		for _, v := range c.Variables {
			if v.ID != varID {
				out = append(out, v)
			}
		}
		cs[i].Variables = out
		saveCollections(cs)
		return
	}
}

// ── HTTP Handlers ──────────────────────────────────────────────────────────

// CollectionsPanelHandler handles GET /collections-panel
// Returns the sidebar collections list (loaded by HTMX).
func CollectionsPanelHandler(c *gin.Context) {
	c.HTML(200, "collections_panel.html", gin.H{
		"Collections": loadCollections(),
	})
}

// CollectionOptionsHandler handles GET /collections/options
// Returns <option> elements for the save-to-collection modal dropdown.
func CollectionOptionsHandler(c *gin.Context) {
	c.HTML(200, "collection_options.html", gin.H{
		"Collections": loadCollections(),
	})
}

// CollectionCreateHandler handles POST /collections
func CollectionCreateHandler(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		name = "New Collection"
	}
	createCollection(name)
	c.Header("HX-Trigger", "collectionsUpdated")
	c.Status(200)
}

// CollectionDeleteHandler handles DELETE /collections/:id
func CollectionDeleteHandler(c *gin.Context) {
	deleteCollection(c.Param("id"))
	c.Header("HX-Trigger", "collectionsUpdated")
	c.Status(200)
}

// CollectionSettingsHandler handles GET /collections/:id/settings
// Returns the collection settings modal content.
func CollectionSettingsHandler(c *gin.Context) {
	coll, ok := getCollection(c.Param("id"))
	if !ok {
		c.Status(404)
		return
	}
	c.HTML(200, "collection_settings.html", gin.H{
		"Collection": coll,
		"Profiles":   loadAuthProfiles(),
	})
}

// CollectionSettingsUpdateHandler handles POST /collections/:id/settings
// Updates a collection's name and default auth profile.
func CollectionSettingsUpdateHandler(c *gin.Context) {
	id := c.Param("id")
	name := strings.TrimSpace(c.PostForm("name"))
	authProfileID := c.PostForm("auth_profile_id")
	updateCollectionMeta(id, name, authProfileID)
	c.Header("HX-Trigger", "collectionsUpdated")

	coll, _ := getCollection(id)
	c.HTML(200, "collection_settings.html", gin.H{
		"Collection": coll,
		"Profiles":   loadAuthProfiles(),
		"Saved":      true,
	})
}

// CollectionSaveRequestHandler handles POST /collections/save-request
// Receives a JSON snapshot of the current form state and saves it to a collection.
func CollectionSaveRequestHandler(c *gin.Context) {
	collID := c.PostForm("collection_id")
	reqName := strings.TrimSpace(c.PostForm("request_name"))
	snapshot := c.PostForm("snapshot")

	if collID == "" {
		c.JSON(200, gin.H{"ok": false, "error": "No collection selected."})
		return
	}
	if reqName == "" {
		reqName = "Untitled Request"
	}

	// Decode the JSON snapshot built by the client
	var snap struct {
		Method        string   `json:"method"`
		URL           string   `json:"url"`
		Headers       []KVPair `json:"headers"`
		Params        []KVPair `json:"params"`
		Body          string   `json:"body"`
		BodyType      string   `json:"body_type"`
		AuthProfileID string   `json:"auth_profile_id"`
	}
	if err := json.Unmarshal([]byte(snapshot), &snap); err != nil {
		c.JSON(200, gin.H{"ok": false, "error": "Could not parse request snapshot."})
		return
	}

	r := SavedRequest{
		Name:          reqName,
		Method:        snap.Method,
		URL:           snap.URL,
		Headers:       snap.Headers,
		Params:        snap.Params,
		Body:          snap.Body,
		BodyType:      snap.BodyType,
		AuthProfileID: snap.AuthProfileID,
	}

	if _, ok := saveRequestToCollection(collID, r); !ok {
		c.JSON(200, gin.H{"ok": false, "error": "Collection not found."})
		return
	}

	c.Header("HX-Trigger", "collectionsUpdated")
	c.JSON(200, gin.H{"ok": true})
}

// CollectionRequestLoadHandler handles GET /collections/:id/requests/:req_id
// Returns form.html pre-filled with the saved request, like HistoryLoadHandler.
func CollectionRequestLoadHandler(c *gin.Context) {
	req, coll, ok := getRequestFromCollection(c.Param("id"), c.Param("req_id"))
	if !ok {
		c.HTML(200, "form.html", defaultFormData())
		return
	}

	headers := req.Headers
	for len(headers) < 2 {
		headers = append(headers, KVPair{"", ""})
	}
	params := req.Params
	if len(params) == 0 {
		params = append(params, KVPair{"", ""})
	}
	bodyType := req.BodyType
	if bodyType == "" {
		bodyType = "json"
	}

	// Auth: use the request's own auth; fall back to the collection default.
	authProfileID := req.AuthProfileID
	if authProfileID == "" {
		authProfileID = coll.AuthProfileID
	}

	c.HTML(200, "form.html", gin.H{
		"Method":        req.Method,
		"URL":           req.URL,
		"Headers":       headers,
		"Params":        params,
		"Body":          req.Body,
		"BodyType":      bodyType,
		"AuthProfileID": authProfileID,
		"CollectionID":  coll.ID,
	})
}

// CollectionRequestDeleteHandler handles DELETE /collections/:id/requests/:req_id
func CollectionRequestDeleteHandler(c *gin.Context) {
	deleteRequestFromCollection(c.Param("id"), c.Param("req_id"))
	c.Header("HX-Trigger", "collectionsUpdated")
	c.Status(200)
}

// CollVarListHandler handles GET /collections/:id/variables
// Returns the collection's variables as JSON (includes IDs needed for delete/patch).
func CollVarListHandler(c *gin.Context) {
	coll, ok := getCollection(c.Param("id"))
	if !ok {
		c.JSON(404, gin.H{"error": "collection not found"})
		return
	}
	if coll.Variables == nil {
		coll.Variables = []CollVar{}
	}
	c.JSON(200, coll.Variables)
}

// CollVarPatchHandler handles PATCH /collections/:id/variables/:var_id
// Updates the value of a collection variable in-place.
func CollVarPatchHandler(c *gin.Context) {
	collID := c.Param("id")
	varID := c.Param("var_id")
	cs := loadCollections()
	for i, coll := range cs {
		if coll.ID != collID {
			continue
		}
		for j, v := range coll.Variables {
			if v.ID == varID {
				cs[i].Variables[j].Value = c.PostForm("value")
				saveCollections(cs)
				c.Header("HX-Trigger", "collectionsUpdated")
				c.Status(200)
				return
			}
		}
	}
	c.Status(404)
}

// CollVarCreateHandler handles POST /collections/:id/variables
func CollVarCreateHandler(c *gin.Context) {
	collID := c.Param("id")
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" || !varRe.MatchString("{{"+name+"}}") {
		coll, _ := getCollection(collID)
		c.HTML(200, "collection_settings.html", gin.H{
			"Collection": coll,
			"Profiles":   loadAuthProfiles(),
			"Error":      "Invalid variable name.",
		})
		return
	}
	upsertCollVar(collID, CollVar{Name: name, Value: c.PostForm("value")})
	c.Header("HX-Trigger", "collectionsUpdated")
	coll, _ := getCollection(collID)
	c.HTML(200, "collection_settings.html", gin.H{
		"Collection": coll,
		"Profiles":   loadAuthProfiles(),
	})
}

// CollVarDeleteHandler handles DELETE /collections/:id/variables/:var_id
func CollVarDeleteHandler(c *gin.Context) {
	collID := c.Param("id")
	deleteCollVar(collID, c.Param("var_id"))
	c.Header("HX-Trigger", "collectionsUpdated")
	coll, _ := getCollection(collID)
	c.HTML(200, "collection_settings.html", gin.H{
		"Collection": coll,
		"Profiles":   loadAuthProfiles(),
	})
}
