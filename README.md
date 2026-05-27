# ⚡ API Playground

A lightweight Postman-style HTTP client that runs entirely in your browser, backed by a Go server that proxies all requests (no CORS issues). Supports auth profiles, request history, and curl import.

---

## Requirements

- [Go 1.21+](https://go.dev/dl/) — check with `go version`

No other installs needed. All frontend dependencies (DaisyUI, Tailwind, HTMX) are loaded from CDN at runtime.

---

## Running

```bash
# 1. Clone or enter the project directory
cd api-playground

# 2. Download Go dependencies
go mod download

# 3. Build and run
go run .
```

Open **http://localhost:8080** in your browser.

> To run a pre-built binary instead:
> ```bash
> go build -o api-playground .
> ./api-playground
> ```

---

## What it does

| Feature | Details |
|---|---|
| **HTTP client** | GET / POST / PUT / DELETE / PATCH with URL, query params, headers, and body |
| **Body types** | JSON, XML, Form URL-encoded, plain text — auto-sets `Content-Type` |
| **Curl import** | Paste any `curl` command and the form fills in automatically |
| **History** | Every request auto-saves to `history.json`; searchable, replayable, deletable |
| **Export / Import** | Download history as JSON; re-import to restore or share |
| **Auth profiles** | Saved credentials injected server-side — never touches CORS |
| **No CORS** | The Go server proxies all outbound requests |

---

## Auth Profiles

Click **🔐 Auth** in the navbar to manage profiles. Four types are supported:

### Bearer Token
Static token injected as `Authorization: Bearer <token>`.

### Basic Auth
Username + password encoded and injected as `Authorization: Basic <base64>`.

### API Key
Injects a key/value pair into a request header or query param.

### OAuth2 (client_credentials)
Fetches an access token from a token URL using `client_credentials` grant. Token is cached and auto-refreshed before expiry.

### Token API ✨ *(custom login endpoint)*
For APIs with their own login endpoint (e.g. `POST /auth/login`). Configure:

| Field | Example |
|---|---|
| Login URL | `https://api.example.com/auth/login` |
| Body | `{"username": "alice", "password": "secret"}` |
| Body Type | JSON or Form URL-encoded |
| Token Path | `access_token` or `data.token` or `result.auth.jwt` |
| Expiry Path | `expires_in` (seconds, optional) |
| Inject as | `Authorization: Bearer <token>` (configurable) |

Hit **Test Login** before saving to verify the config fires correctly and the token path resolves.

Tokens are cached in `auth_profiles.json` and auto-refreshed when within 30 seconds of expiry.

---

## Project layout

```
api-playground/
├── main.go           # router setup, custom template functions
├── handlers.go       # POST /send — proxies requests, injects auth
├── auth.go           # auth profile CRUD, token injection, OAuth2, Token API
├── history.go        # history persistence, export/import, delete
├── curl_parser.go    # curl → form field parser
├── go.mod / go.sum
├── history.json      # auto-created on first request
├── auth_profiles.json # auto-created when you save a profile
└── templates/
    ├── index.html         # shell: navbar, 3-column layout, auth modal
    ├── form.html          # request builder (method, URL, headers, params, body, auth)
    ├── response.html      # response panel + 401 auth suggestion
    ├── history.html       # history sidebar entries
    ├── auth_profiles.html # auth modal content + create form
    └── auth_options.html  # <option> elements for the auth dropdown
```

---

## Data files

Both files are created automatically — you don't need to create them.

| File | Contents |
|---|---|
| `history.json` | Last 100 requests (method, URL, headers, params, body, status, duration) |
| `auth_profiles.json` | Saved auth profiles including cached OAuth2/Token API tokens |

To reset everything: `rm history.json auth_profiles.json`

---

## Keyboard shortcuts

| Action | How |
|---|---|
| Send request | Click **Send Request** button |
| Parse curl | Paste into the curl box → click **Parse →** |
| Replay request | Click any entry in the History sidebar |
| Delete history entry | Hover over an entry → click **✕** |
| Search history | Type in the search box above the history list |
| Open auth profiles | Click **🔐 Auth** in the navbar |
| Export history | Click **⬇** in the history header |
| Import history | Click **⬆** in the history header → pick a `.json` file |
