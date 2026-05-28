package main

import (
	"html/template"
	"time"

	"github.com/gin-gonic/gin"
	webview "github.com/webview/webview_go"
)

func main() {
	// Silence gin logs — the user never sees a terminal
	gin.SetMode(gin.ReleaseMode)

	// Start the HTTP server on a background goroutine
	go startServer()

	// Give it a moment to bind port 8080 before the window tries to load
	time.Sleep(400 * time.Millisecond)

	// Open the native app window (WKWebView on macOS).
	// Must run on the main thread.
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("⚡ API Playground")

	// Size the window to fit the screen.
	// Prefer up to 1400×900 but never exceed the available display area.
	sw, sh := screenSize()
	winW := sw - 40  // 20px breathing room each side
	winH := sh - 60  // 30px top (menu bar) + 30px bottom
	if winW > 1400 { winW = 1400 }
	if winH > 900  { winH = 900  }
	w.SetSize(winW, winH, webview.HintNone)

	w.Navigate("http://localhost:8080")
	w.Run() // blocks until the window is closed
}

func startServer() {
	router := gin.Default()

	// Register custom template functions before loading templates
	router.SetFuncMap(template.FuncMap{
		// truncate cuts a string to n runes and appends "…"
		"truncate": func(s string, n int) string {
			r := []rune(s)
			if len(r) <= n {
				return s
			}
			return string(r[:n]) + "…"
		},
	})

	router.LoadHTMLGlob("templates/*")

	// ── Main UI ────────────────────────────────────────────────────────────
	router.GET("/", func(c *gin.Context) {
		c.HTML(200, "index.html", defaultFormData())
	})

	// ── Request proxy ──────────────────────────────────────────────────────
	router.POST("/send", SendRequest)

	// ── Import parsers ─────────────────────────────────────────────────────
	router.POST("/parse-curl", ParseCurlHandler)
	router.POST("/parse-raw-http", ParseRawHTTPHandler)

	// ── History ────────────────────────────────────────────────────────────
	router.GET("/history-panel", HistoryPanelHandler)
	router.GET("/history/export", HistoryExportHandler) // must come before /:id
	router.GET("/history/:id", HistoryLoadHandler)
	router.DELETE("/history/:id", HistoryDeleteHandler)
	router.POST("/history/import", HistoryImportHandler)

	// ── Auth Profiles ──────────────────────────────────────────────────────
	router.GET("/auth-profiles", AuthProfilesPanelHandler)
	router.GET("/auth-profiles/options", AuthProfilesOptionsHandler)      // before /:id
	router.POST("/auth-profiles/test-login", AuthProfileTestLoginHandler) // before /:id
	router.POST("/auth-profiles", AuthProfileCreateHandler)
	router.DELETE("/auth-profiles/:id", AuthProfileDeleteHandler)

	// ── Variables ──────────────────────────────────────────────────────────────
	router.GET("/variables/map", VariablesMapHandler)   // before /:id
	router.GET("/variables/list", VariablesListHandler) // before /:id
	router.GET("/variables", VariablesPanelHandler)
	router.POST("/variables", VariableCreateHandler)
	router.PATCH("/variables/:id", VariablePatchHandler)
	router.DELETE("/variables/:id", VariableDeleteHandler)

	// ── Collections ────────────────────────────────────────────────────────────
	router.GET("/collections-panel", CollectionsPanelHandler)
	router.GET("/collections/options", CollectionOptionsHandler)                       // before /:id
	router.POST("/collections/save-request", CollectionSaveRequestHandler)             // before /:id
	router.POST("/collections", CollectionCreateHandler)
	router.DELETE("/collections/:id", CollectionDeleteHandler)
	router.GET("/collections/:id/settings", CollectionSettingsHandler)
	router.POST("/collections/:id/settings", CollectionSettingsUpdateHandler)
	router.GET("/collections/:id/variables", CollVarListHandler)
	router.POST("/collections/:id/variables", CollVarCreateHandler)
	router.PATCH("/collections/:id/variables/:var_id", CollVarPatchHandler)
	router.DELETE("/collections/:id/variables/:var_id", CollVarDeleteHandler)
	router.GET("/collections/:id/requests/:req_id", CollectionRequestLoadHandler)      // before /:id
	router.DELETE("/collections/:id/requests/:req_id", CollectionRequestDeleteHandler) // before /:id

	router.Run(":8080")
}
