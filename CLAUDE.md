# CLAUDE.md — API Playground

This file provides Claude Code with the context needed to work effectively in this repository.

## Project summary

API Playground is a native macOS desktop application — a lightweight Postman-style HTTP client. It runs as a `webview_go`-wrapped Go process that opens a WKWebView window pointed at a locally-served Gin web application. All HTTP requests are proxied through the Go server (no CORS), and the UI is built with HTMX + DaisyUI (hypermedia-driven, server-side rendering).

## Build and run

```bash
# Run in development (terminal mode — opens a native window)
go mod download
go run .

# Build a standalone binary
CGO_ENABLED=1 go build -o api-playground .

# Build the macOS .app bundle (required for double-click from Finder)
chmod +x build-app.sh
./build-app.sh
```

**CGO is required.** `screen_darwin.go` calls CoreGraphics via CGO to read the display size. You need Xcode Command Line Tools installed (`xcode-select --install`).

## Project structure

```
api-playground/
├── main.go              # Entry point: WKWebView window + Gin server bootstrap
├── handlers.go          # POST /send — the request proxy engine
├── auth.go              # Token API auth profiles: login, cache, inject, refresh
├── collections.go       # Collections CRUD + saved requests + collection variables
├── variables.go         # Global variables + {{VAR}} / {VAR} expansion engine
├── history.go           # Request history: persist (last 100), export, import, delete
├── curl_parser.go       # curl tokeniser + raw HTTP block parser → form fields
├── environments.go      # Environments system (dormant — not wired into routes)
├── screen_darwin.go     # CGO: CoreGraphics screen size for window sizing
├── build-app.sh         # Assembles "API Playground.app" bundle
├── go.mod / go.sum
└── templates/           # Go html/template files served by Gin
    ├── index.html            # Full shell: layout, sidebar, modals, all JS
    ├── form.html             # Request builder (swapped by HTMX on history/collection load)
    ├── response.html         # Response panel (swapped by HTMX after /send)
    ├── history.html          # History sidebar list entries
    ├── collections_panel.html
    ├── collection_settings.html
    ├── collection_options.html
    ├── variables.html
    ├── auth_profiles.html
    └── auth_options.html
```

## Key architecture rules

1. **Server-side HTML rendering only.** All templates are rendered by Gin's `html/template`. HTMX swaps fragments. There is no client-side routing or virtual DOM.

2. **Variable expansion happens server-side** in `expandVariablesCtx()` (`variables.go`). The JS in `index.html` has a client-side preview-only copy — keep them in sync if the regex changes.

3. **HTMX event bus.** Server responses use `HX-Trigger` response headers (e.g. `historyUpdated`, `collectionsUpdated`) to trigger panel refreshes. Add new triggers to the event table in `docs/FLOW.md` if you introduce new ones.

4. **All data files are relative to the working directory.** In `.app` mode the launcher `cd`s to `~/Library/Application Support/APIPlayground/` before running the binary. In terminal mode, files land in the project root. Never use absolute paths in file I/O.

5. **IDs are nanosecond Unix timestamps** (`fmt.Sprintf("%d", time.Now().UnixNano())`). This is intentional — no UUID dependency, human-readable in JSON.

6. **Port 8080 is hardcoded.** Do not change it without updating `main.go` (the `w.Navigate` URL) and the README.

## Common tasks

### Add a new route

1. Add the handler function to the appropriate `*.go` file (or a new file in the same package).
2. Register the route in `startServer()` in `main.go`.
3. If the route mutates shared data, emit the appropriate `HX-Trigger` header so dependent panels refresh.
4. Add the route to the API routes table in `README.md`.

### Add a new template

1. Create the file in `templates/`.
2. Register it via `router.LoadHTMLGlob("templates/*")` — this glob already picks up all files in the directory.
3. Copy the template file to `API Playground.app/Contents/Resources/templates/` and re-run `build-app.sh` to bundle it.

### Add a new data type

1. Define the struct in an appropriate `*.go` file.
2. Follow the existing persistence pattern: `loadX()` / `saveX()` / CRUD functions.
3. Use `time.Now().UnixNano()` for IDs.
4. All reads are full-file reads; all writes are full-file writes. This is acceptable for the expected data volumes.

### Modify variable expansion

Both `expandVariablesCtx()` in `variables.go` and the client-side `expandVarsPreview()` function in `templates/index.html` implement the same regex logic. Update both if the placeholder syntax changes.

## Data files

| File | Contents | Cap |
|---|---|---|
| `history.json` | Last 100 HTTP requests | 100 entries |
| `auth_profiles.json` | Auth profiles + cached tokens | Unbounded |
| `variables.json` | Global variables | Unbounded |
| `collections.json` | Collections, requests, collection variables | Unbounded |
| `environments.json` | Environments (dormant) | — |
| `settings.json` | Active environment ID (dormant) | — |

Reset all data: `rm history.json auth_profiles.json variables.json collections.json`

## Known issues / tech debt

- `environments.go` is fully implemented but its HTTP handlers are not registered in `main.go`. It is dead code from an earlier design iteration. Collection-scoped variables supersede it.
- The local server has no authentication. Any local process can call `POST /send` to proxy requests through configured auth profiles. See `docs/SECURITY.md`.
- Response bodies are truncated at 50,000 characters. Large payloads must be fetched via curl outside the app.
- CGO makes cross-compilation and non-macOS builds impossible.

## Documentation

| File | Contents |
|---|---|
| `README.md` | User-facing: install, run, features, API routes, data files |
| `CLAUDE.md` | This file — developer context for Claude Code |
| `docs/ARCHITECTURE.md` | Component map, startup sequence, data model, template rendering |
| `docs/FLOW.md` | Data flow diagrams for every major user action |
| `docs/DESIGN.md` | Design decisions and trade-off rationale |
| `docs/SECURITY.md` | Threat model, input entry points, known risks, recommendations |
