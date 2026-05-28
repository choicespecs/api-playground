# Data Flow

This document describes how data moves through API Playground for every major user action.

## 1. Sending an HTTP request

```mermaid
sequenceDiagram
    participant UI as Browser (WKWebView)
    participant GIN as Gin /send handler
    participant VARS as Variable expander
    participant AUTH as Auth injector
    participant EXT as External API

    UI->>GIN: POST /send (form fields)
    Note over UI,GIN: method, url, body, body_type,\nheader_key[], header_val[],\nparam_key[], param_val[],\nauth_profile_id, collection_id

    GIN->>VARS: expandVariablesCtx(url, collectionID)
    VARS->>VARS: buildVarMap: load globals + collection vars
    VARS-->>GIN: expanded url

    GIN->>VARS: expandVariablesCtx(body, ...)
    VARS-->>GIN: expanded body

    GIN->>VARS: expandVariablesCtx(each header val, ...)
    VARS-->>GIN: expanded header values

    GIN->>GIN: Inherit collection default auth if none set
    GIN->>GIN: Validate URL; auto-prefix https://
    GIN->>GIN: Check for unresolved {{PLACEHOLDER}}
    GIN->>GIN: Append query params to URL

    GIN->>AUTH: injectAuth(req, profileID, force=false)
    AUTH->>AUTH: getAuthProfile(profileID)
    AUTH->>AUTH: Check cached token validity (ExpiresAt > now+30s)
    alt Token expired or missing
        AUTH->>EXT: POST login URL (credentials)
        EXT-->>AUTH: JSON with token
        AUTH->>AUTH: extractJSONPath(token_path)
        AUTH->>AUTH: Update ExpiresAt, write auth_profiles.json
    end
    AUTH-->>GIN: token set on Authorization header

    GIN->>EXT: Proxied HTTP request (30s timeout)
    EXT-->>GIN: Response

    alt Response is 401 and auth profile set
        GIN->>AUTH: injectAuth(req, profileID, force=true)
        AUTH->>EXT: POST login URL (force refresh)
        EXT-->>AUTH: Fresh token
        AUTH-->>GIN: new token on header
        GIN->>EXT: Retry request
        EXT-->>GIN: Response
    end

    GIN->>GIN: Read body, pretty-print JSON, truncate > 50KB
    GIN->>GIN: addToHistory(entry) → history.json
    GIN-->>UI: response.html HTML snippet
    Note over UI: HTMX swaps into #response-body
    UI->>UI: Dispatch HX-Trigger: historyUpdated
    UI->>GIN: GET /history-panel
    GIN-->>UI: history.html snippet
```

## 2. curl / raw HTTP import

```mermaid
sequenceDiagram
    participant UI as Browser
    participant GIN as Gin
    participant PARSER as curl_parser.go

    alt Paste into URL bar
        UI->>UI: handleUrlPaste(evt): detect curl or HTTP
        UI->>GIN: POST /parse-curl  {curl_command: "..."}
    else Import modal
        UI->>UI: detectFormat(text): auto-switch tab
        UI->>GIN: POST /parse-curl or /parse-raw-http
    end

    GIN->>PARSER: parseCurl(cmd) or parseRawHTTP(raw)
    PARSER->>PARSER: tokenize(): handle quotes, continuations
    PARSER->>PARSER: Extract method, URL, headers, body
    PARSER->>PARSER: -u/--user: base64 → Authorization header
    PARSER->>PARSER: url.Parse: split query params into Params[]
    PARSER->>PARSER: bodyTypeFromHeaders(Content-Type)
    PARSER-->>GIN: ParsedCurl{Method, URL, Headers, Params, Body}

    GIN-->>UI: form.html (pre-filled request form)
    Note over UI: HTMX outerHTML swap of #request-form
```

## 3. Variable resolution

```mermaid
flowchart TD
    subgraph Client["Client-side (live preview only)"]
        A["URL input: oninput"] --> B["updateVarPreview()"]
        B --> C["expandVarsPreview() — regex over _varsMap"]
        C --> D["Show #url-var-preview span"]

        E["Type {{ in any field"] --> F["_checkVarTrigger()"]
        F --> G["Filter _varsMap names by prefix"]
        G --> H["_showVarAC() floating picker"]
        H --> I["User selects → _pickVarAC()"]
        I --> J["Insert {{NAME}} at cursor"]
    end

    subgraph Server["Server-side (authoritative)"]
        K["POST /send"] --> L["buildVarMap(collectionID)"]
        L --> M["Load variables.json\n(globals)"]
        L --> N{"collectionID?"}
        N -- "yes" --> O["Load collections.json\nfind matching collection vars"]
        O --> P["Merge: coll overrides global"]
        N -- "no" --> P
        M --> P
        P --> Q["varRe: replace {{NAME}}"]
        Q --> R["varReSingle: replace {NAME}\n(only if name exists in map)"]
        R --> S["Expanded value"]
    end

    subgraph Panel["Right sidebar refresh"]
        T["variablesUpdated or collectionsUpdated event"] --> U["refreshVarsMap()"]
        U --> V["GET /variables/map?collection_id=..."]
        U --> W["GET /variables/list"]
        U --> X{"activeCollID set?"}
        X -- "yes" --> Y["GET /collections/:id/variables"]
        Y --> Z["updateVarsPanel()"]
        V --> Z
        W --> Z
    end
```

## 4. Auth token acquisition and injection

```mermaid
sequenceDiagram
    participant H as handlers.go (SendRequest)
    participant A as auth.go (injectAuth)
    participant FS as auth_profiles.json
    participant LOGIN as Login API endpoint

    H->>A: injectAuth(req, profileID, force=false)
    A->>FS: loadAuthProfiles()
    FS-->>A: []AuthProfile
    A->>A: find profile by ID

    alt force=false AND AccessToken != "" AND ExpiresAt > now+30
        A-->>H: Token from cache (no network call)
    else Token missing, expired, or force=true
        A->>A: expandVariables(LoginURL, LoginBody)
        A->>LOGIN: HTTP request (method, body, headers from profile)
        LOGIN-->>A: JSON response

        alt Status >= 400
            A-->>H: error: "login API returned N: ..."
        else
            A->>A: extractJSONPath(response, TokenPath)
            A->>A: extractJSONPath(response, ExpiryPath) → ExpiresAt
            A->>FS: updateAuthProfile(profile with new token+expiry)
            A-->>H: token string
        end
    end

    H->>H: req.Header.Set(TokenHeaderName, prefix+token)
```

## 5. Collection request save / load

```mermaid
sequenceDiagram
    participant UI as Browser
    participant GIN as Gin
    participant FS as collections.json

    Note over UI: User clicks Save in toolbar
    UI->>UI: _collectFormSnapshot() — read all form fields
    UI->>UI: openSaveToCollectionModal()
    UI->>GIN: GET /collections/options (HTMX, populates select)
    GIN->>FS: loadCollections()
    FS-->>GIN: []Collection
    GIN-->>UI: option elements

    UI->>GIN: POST /collections/save-request\n{collection_id, request_name, snapshot(JSON)}
    GIN->>GIN: json.Unmarshal(snapshot)
    GIN->>FS: loadCollections → find collection → prepend SavedRequest
    FS-->>GIN: updated
    GIN-->>UI: {ok: true}
    UI->>UI: Dispatch collectionsUpdated → HTMX reloads sidebar

    Note over UI: User clicks saved request in sidebar
    UI->>GIN: GET /collections/:id/requests/:req_id
    GIN->>FS: getRequestFromCollection(collID, reqID)
    FS-->>GIN: SavedRequest + Collection (for default auth fallback)
    GIN-->>UI: form.html pre-filled
    Note over UI: HTMX outerHTML swap of #request-form
    UI->>UI: _activeCollID = collID → refreshVarsMap()
```

## 6. History persistence

```mermaid
flowchart TD
    A["Every POST /send\naddToHistory(entry)"] --> B["loadHistory()\nread history.json"]
    B --> C["Prepend new entry\n(newest first)"]
    C --> D{"len > 100?"}
    D -- "yes" --> E["Trim to 100 entries"]
    D -- "no" --> F
    E --> F["saveHistory()\nwrite history.json"]

    G["GET /history-panel"] --> H["loadHistory()\nrender history.html with all entries"]

    I["GET /history/:id"] --> J["loadHistory()\nfind entry by ID"]
    J --> K["Render form.html pre-filled"]

    L["GET /history/export"] --> M["os.ReadFile(history.json)\nserve as attachment download"]

    N["POST /history/import\nmultipart file upload"] --> O["json.Decode uploaded file"]
    O --> P["Deduplicate by ID\n(imported first, then existing)"]
    P --> Q["Trim to 100 entries\nsaveHistory()"]

    R["DELETE /history/:id"] --> S["Filter out entry by ID\nsaveHistory()"]
```

## 7. Panel resize drag

```mermaid
flowchart TD
    A["mousedown on #split-handle"] --> B["Record startY + startReqH"]
    B --> C["Set cursor: row-resize\nSet userSelect: none"]
    C --> D["mousemove events"]
    D --> E["delta = clientY - startY"]
    E --> F["newH = clamp(startReqH + delta, 80, totalH-80)"]
    F --> G["request-area height = newH/totalH * 100%\nflex = none"]
    G --> D
    D -- "mouseup" --> H["dragging = false\nRestore cursor + userSelect"]
```

## 8. HTMX event bus

HTMX custom events are used as a lightweight pub/sub system so that server responses can trigger UI refreshes across independent panel elements without point-to-point coupling.

```mermaid
flowchart TD
    subgraph Producers["Server response headers (HX-Trigger)"]
        P1["POST /send → historyUpdated"]
        P2["DELETE /history/:id → historyUpdated"]
        P3["POST /history/import → historyUpdated"]
        P4["POST /collections → collectionsUpdated"]
        P5["DELETE /collections/:id → collectionsUpdated"]
        P6["POST /collections/save-request → collectionsUpdated"]
        P7["POST /collections/:id/settings → collectionsUpdated"]
        P8["PATCH /collections/:id/variables/:var_id → collectionsUpdated"]
        P9["POST /variables → variablesUpdated"]
        P10["PATCH /variables/:id → variablesUpdated"]
        P11["DELETE /variables/:id → variablesUpdated"]
        P12["POST /auth-profiles → authProfilesUpdated"]
        P13["DELETE /auth-profiles/:id → authProfilesUpdated"]
    end

    subgraph Consumers["Elements listening on body (hx-trigger or addEventListener)"]
        C1["#history-list-container\nhx-trigger='historyUpdated from:body'\n→ GET /history-panel"]
        C2["#collections-list\nhx-trigger='collectionsUpdated from:body'\n→ GET /collections-panel"]
        C3["#vars-modal-content\nhx-trigger='variablesUpdated from:body'\n→ GET /variables"]
        C4["#auth-modal-content\nhx-trigger='authProfilesUpdated from:body'\n→ GET /auth-profiles"]
        C5["JS: body.addEventListener('variablesUpdated')\n→ refreshVarsMap()"]
        C6["JS: body.addEventListener('collectionsUpdated')\n→ refreshVarsMap()"]
    end

    P1 --> C1
    P2 --> C1
    P3 --> C1
    P4 --> C2
    P5 --> C2
    P6 --> C2
    P7 --> C2
    P8 --> C2
    P9 --> C3
    P9 --> C5
    P10 --> C3
    P10 --> C5
    P11 --> C3
    P11 --> C5
    P12 --> C4
    P13 --> C4
    P4 --> C6
    P5 --> C6
    P6 --> C6
```
