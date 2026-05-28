# Design Decisions

This document records the significant design choices made in API Playground, the reasoning behind each, and known trade-offs or tech-debt items.

---

## 1. Hypermedia-first architecture (HTMX over SPA)

**Decision:** Use HTMX for all UI updates. The server returns HTML fragments; the browser never manages application state via JSON APIs.

**Rationale:**
- The Go backend already renders templates. Returning partial HTML from the same templates eliminates a separate API contract layer.
- HTMX's `hx-trigger` / `HX-Trigger` event bus gives clean pub/sub decoupling (e.g. `historyUpdated` triggers panel refresh from any mutation endpoint) without Redux-style state management.
- The WKWebView host is a known single-user desktop environment — there is no need for CDN-optimised JavaScript bundles or offline-first service workers.

**Trade-off:** Complex interactive behaviours (autocomplete, drag-resize, curl generator, right-panel variable editing) still require vanilla JS written directly in `index.html`. This JS cannot be unit-tested in isolation.

---

## 2. Server-side variable expansion

**Decision:** All `{{VAR}}` and `{VAR}` substitution happens inside `SendRequest` on the Go server, not in the browser.

**Rationale:**
- Keeps the browser form values as the user typed them (tokens remain visible).
- A single authoritative expansion path means consistent behaviour across history replay, collection load, and direct form submission.
- Prevents the browser from ever sending raw secrets to the WKWebView's JavaScript context.

**Trade-off:** The client-side URL preview (`updateVarPreview`) replicates the same regex logic in JS. If the regex changes server-side it must be updated in two places. A comment in `variables.go` and `index.html` documents this coupling.

---

## 3. ID generation via `time.Now().UnixNano()`

**Decision:** All entity IDs (collections, variables, history entries, auth profiles) are nanosecond Unix timestamps formatted as decimal strings.

**Rationale:**
- Zero dependencies — no UUID library required.
- Monotonically increasing in practice (desktop app, single goroutine for writes).
- Human-readable for debugging the raw JSON files.

**Trade-off:** Not globally unique across machines. Importing history from a different machine and then running the app on the original machine could theoretically produce a collision if two entries were created within the same nanosecond. In practice the 100-entry cap and file-based deduplication (`HistoryImportHandler` deduplicates by ID) keep this risk negligible.

---

## 4. File-per-concern JSON persistence (no database)

**Decision:** Each data type lives in its own flat JSON file (`history.json`, `variables.json`, `collections.json`, `auth_profiles.json`). There is no SQLite or embedded database.

**Rationale:**
- Zero-dependency, human-readable persistence suitable for a single-user desktop tool.
- Files are trivially inspectable, editable by hand, and version-controllable.
- The 100-entry history cap bounds `history.json` size. Collection and variable lists are expected to stay small.

**Trade-off:** Every write is a full file read → mutate → write. This is fine for expected data volumes (hundreds of entries at most) but would not scale to thousands. There is no write batching or locking; concurrent writes from two windows would corrupt the files. (Only one window is ever opened by the launcher.)

**Tech debt:** `environments.go` implements a full environment/variable system (`loadEnvironments`, `upsertEnvVar`, etc.) that is not wired into any HTTP routes in `main.go` and is therefore dead code. It was an earlier design iteration superseded by the collection-scoped variable approach.

---

## 5. Token API as the only auth profile type

**Decision:** Auth profiles support only the "Token API" flow (call a login endpoint, extract a token, cache it, inject it). Static bearer tokens or API keys are handled via `{{VARIABLE}}` in headers.

**Rationale:**
- The Token API pattern covers the most common "authenticate first" APIs (OAuth-adjacent, proprietary login flows, session tokens).
- Static credentials are trivially handled by defining a `TOKEN` variable and writing `Authorization: Bearer {{TOKEN}}` in the headers tab — no special UI needed.
- Supporting OAuth 2.0 PKCE or client-credentials would require a browser redirect or client-secret management, which significantly increases scope and surface area.

**Trade-off:** Users with Basic Auth (username:password) must base64-encode manually or use the curl import (`-u user:pass` is parsed and converted to an `Authorization: Basic ...` header by `parseCurl`).

---

## 6. Token caching with 30-second buffer and 401 auto-retry

**Decision:** Cached tokens are considered valid only when `ExpiresAt > now + 30`. On a downstream 401, the token is force-refreshed and the request retried exactly once.

**Rationale:**
- The 30-second buffer prevents sending a token that will expire in-flight.
- The single 401 retry handles the case where the API invalidates tokens server-side before the local `ExpiresAt` (e.g. logout from another client, token rotation policies).
- A single retry is sufficient and avoids infinite loops.

**Trade-off:** Two HTTP calls happen in series when a refresh is needed (login + original request). The 30s UI timeout (`http.Client{Timeout: 30 * time.Second}`) applies independently to both calls, so a slow login endpoint could extend total wait time to ~60s.

---

## 7. 50 KB response body cap

**Decision:** Response bodies are truncated at 50,000 characters before being injected into the template (`const maxDisplay = 50_000`).

**Rationale:**
- WKWebView renders the response inside a `<pre>` tag. Injecting megabytes of text causes visible janking and can crash the view on underpowered hardware.
- The truncation is clearly signalled to the user via a `badge-warning` "truncated" badge.

**Trade-off:** Users cannot see the full body of large responses in-app. They must replay the request via curl (the "curl" button generates the command) to capture the full output. A future improvement would be a "download raw response" button.

---

## 8. CGO for screen sizing

**Decision:** `screen_darwin.go` uses CGO to call `CGDisplayBounds` from CoreGraphics rather than using a fixed default window size.

**Rationale:**
- The window is sized to fit the actual display (up to 1400×900) with a 20px/30px breathing room. On small laptop screens (e.g. 1280×800) a fixed 1400×900 would overflow.
- CoreGraphics returns logical points (already DPI-scaled), so Retina displays report the correct "looks like" resolution without additional calculation.

**Trade-off:** `CGO_ENABLED=1` is required for the build. This means `go run .` requires Xcode Command Line Tools. A pure-Go fallback could default to `1200×750` but would not adapt to small or ultrawide displays.

---

## 9. Paste detection in the URL bar

**Decision:** The URL input's `onpaste` handler (`handleUrlPaste`) intercepts paste events. If the pasted text looks like a `curl` command or a raw HTTP block, it is silently rerouted to the import parser — the user never has to open the Import modal explicitly.

**Rationale:**
- Reduces friction for the most common import workflow (copy curl from a browser DevTools "Copy as cURL" action, paste into URL bar).
- Auto-detection (`/^curl[\s\t]/` or `/^(GET|POST|...)\s/`) covers the overwhelming majority of real-world cases.

**Trade-off:** A URL that starts with a word followed by a space (e.g. a mistaken paste of `DELETE https://...`) triggers the raw HTTP parser rather than being treated as a URL fragment. This is considered acceptable — a real URL never starts with an HTTP method word.

---

## 10. No authentication on the local HTTP server

**Decision:** The Gin server listens on `localhost:8080` with no authentication, CORS restrictions, or origin checks.

**Rationale:**
- The server is only reachable from the loopback interface. Other processes on the same machine could reach it, but this is consistent with how Postman, Insomnia, and similar tools operate locally.
- WKWebView loads `http://localhost:8080` directly — adding token-based auth to the local server would require injecting the token into the webview, creating more attack surface than it prevents.

**Trade-off:** Any local process can call `POST /send` to proxy requests through the app's auth profiles, potentially leaking tokens. This is a known, accepted risk for a desktop developer tool. See `SECURITY.md` for full discussion.

---

## 11. Template loading from current working directory

**Decision:** `router.LoadHTMLGlob("templates/*")` resolves relative to the process working directory, not the binary location.

**Rationale:**
- In terminal mode (`go run .`) the working directory is the project root, which contains `templates/`.
- The launcher script `cd`s into the DATA directory and copies templates there before executing the binary, so the relative path still resolves correctly inside the `.app` bundle.

**Trade-off:** Running the compiled binary from an arbitrary directory (e.g. `cd /tmp && /path/to/api-playground`) will fail because `templates/` does not exist in `/tmp`. The README documents that the binary must be run from the project root or via the launcher.

---

## 12. Collection variables override global variables

**Decision:** When a collection request is loaded, its collection variables shadow global variables with the same name. Resolution order (lower priority first): global → collection.

**Rationale:**
- Enables collections to define environment-specific values (e.g. `BASE_URL=https://staging.api.example.com`) that override a global default (`BASE_URL=https://api.example.com`) without modifying the global.
- Mirrors how Postman handles collection and environment variable precedence.

**Trade-off:** When a user edits a collection variable in the right panel and a global with the same name also exists, both are shown but the collection value wins. The "coll" badge in the UI panel labels which variables are collection-scoped to avoid confusion.
