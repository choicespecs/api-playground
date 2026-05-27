package main

import (
	"html/template"

	"github.com/gin-gonic/gin"
)

func main() {
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

	router.Run(":8080")
}
