# Architecture

API Playground is a native macOS desktop application built from a Go backend server and an HTMX/DaisyUI frontend, packaged into a standard `.app` bundle via `webview_go` (WKWebView).

## High-level component map

```mermaid
flowchart TD
    subgraph macOS["macOS .app Bundle"]
        launcher["launcher (shell script)\nSets DATA dir, syncs templates"]
        binary["api-playground (Go binary)\nMain goroutine: WKWebView window\nBackground goroutine: HTTP server"]
        launcher --> binary
    end

    subgraph server["Go HTTP Server — localhost:8080 (Gin)"]
        main["main.go\nwindow + server bootstrap"]
        handlers["handlers.go\nPOST /send — proxy engine"]
        auth["auth.go\nToken API profiles + token cache"]
        collections["collections.go\nCollections + saved requests"]
        variables["variables.go\nGlobal vars + {{VAR}} expansion"]
        history["history.go\nRequest history (last 100)"]
        curl_parser["curl_parser.go\ncurl + raw HTTP import"]
        environments["environments.go\nEnvironments (dormant)"]
        screen["screen_darwin.go\nCGO CoreGraphics screen sizing"]
    end

    subgraph templates["Go HTML Templates (Gin)"]
        index["index.html\nShell: layout, sidebar, modals, all JS"]
        form["form.html\nRequest builder (method/URL/headers/params/body/auth)"]
        response["response.html\nResponse panel (status/body/headers)"]
        history_tmpl["history.html\nHistory sidebar entries"]
        collections_panel["collections_panel.html\nCollections sidebar"]
        coll_settings["collection_settings.html\nCollection settings modal"]
        variables_tmpl["variables.html\nVariables modal"]
        auth_profiles["auth_profiles.html\nAuth profiles modal"]
        auth_options["auth_options.html\noption elements for auth select"]
        coll_options["collection_options.html\noption elements for collection select"]
    end

    subgraph data["JSON Data Files"]
        history_json["history.json\nLast 100 requests"]
        auth_json["auth_profiles.json\nProfiles + cached tokens"]
        vars_json["variables.json\nGlobal variables"]
        colls_json["collections.json\nCollections, requests, variables"]
        settings_json["settings.json\nActive environment ID"]
    end

    subgraph frontend["Browser (WKWebView)"]
        htmx["HTMX 1.9\nHypermedia-driven UI updates"]
        daisy["DaisyUI 4 + Tailwind\nComponent library + utility CSS"]
        js["Vanilla JS in index.html\nAutocomplete, preview, drag-resize,\nvar panel, curl generator"]
    end

    binary -->|"starts"| server
    binary -->|"opens"| frontend
    frontend -->|"HTTP requests"| server
    server -->|"HTML snippets (hx-swap)"| frontend
    server -->|"reads/writes"| data
    auth -->|"GET/POST login URL"| external["External APIs"]
    handlers -->|"proxies"| external
```

## Runtime startup sequence

```mermaid
sequenceDiagram
    participant OS as macOS
    participant L as launcher (shell)
    participant B as Go binary (main)
    participant S as Gin HTTP server
    participant W as WKWebView

    OS->>L: User opens .app / double-click
    L->>L: mkdir ~/Library/Application Support/APIPlayground
    L->>L: rsync templates from bundle → DATA dir
    L->>B: exec api-playground (cwd = DATA dir)
    B->>S: go startServer() [background goroutine]
    S->>S: router.LoadHTMLGlob("templates/*")
    S->>S: router.Run(":8080")
    B->>B: time.Sleep(400ms)
    B->>W: webview.New() + SetTitle + SetSize
    W->>S: GET http://localhost:8080/
    S->>W: index.html (full shell)
    W->>S: hx-get="/history-panel" (HTMX on load)
    W->>S: hx-get="/collections-panel" (HTMX on load)
    W->>S: fetch /variables/map (JS on load)
    W->>B: w.Run() [blocks main goroutine until window closed]
```

## Request proxy flow (POST /send)

```mermaid
flowchart TD
    A["Browser form submit\nhx-post='/send'"] --> B["Read form fields\nmethod, url, body, body_type\nheader_key[], header_val[]\nparam_key[], param_val[]\nauth_profile_id, collection_id"]
    B --> C["expandVariablesCtx()\nGlobal vars → Collection vars\nDouble-brace + single-brace"]
    C --> D{"auth_profile_id\nempty?"}
    D -- "empty + collection set" --> E["Inherit collection\ndefault auth profile"]
    D -- "already set" --> F
    E --> F["Validate URL\nauto-prefix https://"]
    F --> G{"Unresolved\n{{VAR}}?"}
    G -- "yes" --> ERR1["Return error HTML\nto response panel"]
    G -- "no" --> H["Append query params\nto URL"]
    H --> I["http.NewRequest()\nAttach headers"]
    I --> J["injectAuth()\nToken API: fetch/cache token\nSet Authorization header"]
    J --> K["http.Client.Do()\n30s timeout"]
    K --> L{"HTTP 401?"}
    L -- "yes + auth profile" --> M["Force-refresh token\nRetry request once"]
    M --> N
    L -- "no" --> N["Read response body\nPretty-print JSON\nTruncate > 50 KB"]
    N --> O["addToHistory()\nSave entry to history.json"]
    O --> P["Render response.html\nStatus, Duration, Size, Body, Headers"]
    P --> Q["Return HTML snippet\nhx-swap='innerHTML' into #response-body"]
```

## Variable expansion architecture

```mermaid
flowchart TD
    A["Request field value\ne.g. 'Bearer {{TOKEN}}'"] --> B["buildVarMap(collectionID)"]
    B --> C["Load globals\nvariables.json"]
    B --> D{"collectionID\nset?"}
    D -- "yes" --> E["Load collection vars\ncollections.json"]
    E --> F["Merge: collection\noverrides global"]
    D -- "no" --> G["Global map only"]
    C --> F
    F --> H["varRe.ReplaceAll\n{{NAME}} double-brace"]
    G --> H
    H --> I["varReSingle.ReplaceAll\n{NAME} single-brace\n(only if name in map)"]
    I --> J["Expanded string"]

    K["Client-side JS\nexpandVarsPreview()"] --> L["Same regex, same map\nfrom GET /variables/map\nLIVE preview only — not authoritative"]
```

## Auth profile token lifecycle

```mermaid
stateDiagram-v2
    [*] --> NoToken: Profile created
    NoToken --> Fetching: injectAuth() called
    Fetching --> Cached: Login API 2xx\nToken extracted + ExpiresAt set
    Fetching --> Error: Login API 4xx/5xx\nor JSON parse failure
    Cached --> Valid: ExpiresAt > now + 30s
    Valid --> Injected: Token set on request header
    Cached --> Stale: ExpiresAt ≤ now + 30s
    Stale --> Fetching: force=false triggers refresh
    Injected --> [*]: Request sent
    Injected --> ForcedRefresh: Downstream API returns 401
    ForcedRefresh --> Fetching: injectAuth(force=true)
```

## Data model relationships

```mermaid
erDiagram
    COLLECTION {
        string ID
        string Name
        string AuthProfileID
        string CreatedAt
    }
    COLLECTION ||--o{ SAVED_REQUEST : contains
    COLLECTION ||--o{ COLL_VAR : has

    SAVED_REQUEST {
        string ID
        string Name
        string Method
        string URL
        string Body
        string BodyType
        string AuthProfileID
        string CreatedAt
    }
    SAVED_REQUEST ||--o{ KV_PAIR : headers
    SAVED_REQUEST ||--o{ KV_PAIR : params

    COLL_VAR {
        string ID
        string Name
        string Value
    }

    VARIABLE {
        string ID
        string Name
        string Value
        string CreatedAt
    }

    AUTH_PROFILE {
        string ID
        string Name
        string LoginURL
        string LoginBodyType
        string TokenPath
        string ExpiryPath
        string TokenHeaderName
        string TokenPrefix
        string AccessToken
        int64 ExpiresAt
        string CreatedAt
    }

    HISTORY_ENTRY {
        string ID
        string Method
        string URL
        string Body
        string BodyType
        string AuthProfileID
        int Status
        string Duration
        string CreatedAt
    }
    HISTORY_ENTRY ||--o{ KV_PAIR : headers
    HISTORY_ENTRY ||--o{ KV_PAIR : params

    KV_PAIR {
        string Key
        string Val
    }
```

## Template rendering model

All HTML is server-side rendered via Go's `html/template` package through Gin. HTMX drives partial page updates by swapping targeted DOM fragments — there is no client-side routing or virtual DOM.

| Trigger | Endpoint | Target element | Swap mode |
|---|---|---|---|
| Page load | `GET /` | Full page | Full page |
| HTMX `hx-trigger="load"` | `GET /history-panel` | `#history-list-container` | `innerHTML` |
| HTMX `hx-trigger="load"` | `GET /collections-panel` | `#collections-list` | `innerHTML` |
| HTMX `hx-trigger="load"` | `GET /auth-profiles/options` | `#auth-select` | `innerHTML` |
| HTMX `hx-trigger="load"` | `GET /collections/options` | `#save-collection-select` | `innerHTML` |
| Send button click | `POST /send` | `#response-body` | `innerHTML` |
| History entry click | `GET /history/:id` | `#request-form` | `outerHTML` |
| Collection request click | `GET /collections/:id/requests/:req_id` | `#request-form` | `outerHTML` |
| Import modal submit | `POST /parse-curl` or `/parse-raw-http` | `#request-form` | `outerHTML` |
| `historyUpdated` event | `GET /history-panel` | `#history-list-container` | `innerHTML` |
| `collectionsUpdated` event | `GET /collections-panel` | `#collections-list` | `innerHTML` |

Custom `HX-Trigger` response headers (e.g. `historyUpdated`, `collectionsUpdated`, `variablesUpdated`, `authProfilesUpdated`) propagate state changes to listening elements without requiring the caller to know which panels need refreshing.

## File locations at runtime

| Mode | Data directory |
|---|---|
| `.app` bundle | `~/Library/Application Support/APIPlayground/` |
| Terminal (`go run .` or binary) | Current working directory |

The launcher script handles the `.app` case by `cd`-ing to the data directory before `exec`-ing the binary. The binary always reads/writes JSON files relative to its current working directory.

## Key design constraints

- **CGO required**: `screen_darwin.go` uses CGO to call CoreGraphics `CGDisplayBounds`. The build must use `CGO_ENABLED=1`.
- **macOS only**: `webview_go` uses WKWebView, which is macOS/iOS-only. The binary will not run on other platforms.
- **Port 8080 hardcoded**: The local server always binds to `:8080`. Running two instances simultaneously will fail with "address already in use".
- **No auth header exposure**: Auth token injection happens entirely server-side (`injectAuth`). The browser never sees the raw token value.
- **50 KB response cap**: Response bodies are truncated at 50,000 characters before rendering to prevent the WKWebView from locking up on large payloads.
