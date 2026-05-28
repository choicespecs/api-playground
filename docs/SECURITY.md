# Security

API Playground is a single-user local desktop developer tool. Its threat model is fundamentally different from a web-facing service. This document describes the security properties of the application, known risks, and recommendations.

---

## Threat model

| Asset | Threat | In-scope? |
|---|---|---|
| Auth profile tokens (cached in `auth_profiles.json`) | Local file system access by other processes | Yes |
| Variables (in `variables.json`) | Local file system access | Yes |
| Proxied external API responses | Man-in-the-middle on outbound network | Partial |
| Local server (`localhost:8080`) | Other processes on the same machine calling the proxy | Yes |
| WKWebView JavaScript context | XSS via malicious API response body | Low — mitigated |

The application is not designed to be run on a shared server or in a multi-user environment.

---

## Authentication mechanisms

### Auth profiles — Token API flow

Auth profiles are the primary authentication mechanism for external APIs. The flow:

1. The user configures a login endpoint URL, credentials body, token extraction path, and token header name.
2. On each proxied request, `injectAuth()` in `auth.go` calls the login endpoint (if the cached token is missing or within 30 seconds of expiry), extracts the token via dot-notation JSON path, and injects it into the outbound request header **server-side**.
3. The browser (WKWebView JavaScript context) never receives the raw token value. HTMX only receives the rendered HTML response from `/send`.
4. Tokens are cached on disk in `auth_profiles.json` along with their `ExpiresAt` Unix timestamp.

**Risk:** `auth_profiles.json` stores tokens in plaintext. Any process with file system read access (user-level) can read cached tokens.

**Recommendation:** On macOS, the data directory (`~/Library/Application Support/APIPlayground/`) has standard user-level permissions (700). Do not store production credentials (admin tokens, service account keys) in auth profiles without understanding this risk. Use short-lived tokens or APIs that support token refresh.

### Variables — static credentials

Variables (`variables.json`) often hold API keys, bearer tokens, or other secrets in plaintext.

**Risk:** Same as above — file is world-readable by the current user. Secrets in variables are not encrypted at rest.

**Recommendation:** For highly sensitive credentials, prefer short-lived tokens via the Token API auth profile flow over long-lived API keys in variables.

---

## Input entry points

| Entry point | What is accepted | Sanitisation |
|---|---|---|
| URL bar (`POST /send`) | Arbitrary URL string | Validated to start with `http://` or `https://` after variable expansion; unresolved `{{VAR}}` placeholders are rejected with a clear error |
| Headers (`header_key[]`, `header_val[]`) | Arbitrary key/value strings | Variables expanded; no further sanitisation — values are passed directly to `http.NewRequest` |
| Query params (`param_key[]`, `param_val[]`) | Arbitrary key/value strings | Variables expanded; appended via `url.Parse` + `Query().Set()` which URL-encodes values |
| Body (`body`) | Arbitrary string | Variables expanded; passed as-is to `io.Reader` for the outbound request |
| curl import (`POST /parse-curl`) | Raw curl command string | Parsed by `tokenize()` + `parseCurl()`; no shell execution — the curl string is tokenised in pure Go |
| Raw HTTP import (`POST /parse-raw-http`) | Raw HTTP request text | Parsed by `parseRawHTTP()`; no shell execution |
| History import (`POST /history/import`) | JSON file upload | Decoded with `json.Decoder`; deduplicated by ID string comparison |
| Auth profile login body | Arbitrary string (credentials) | Variables expanded; passed as-is to the login endpoint body |
| Variable name validation | Name string | Validated against `varRe` (`/{{([A-Za-z_][A-Za-z0-9_]*)}}/`) — must start with letter or underscore, alphanumeric/underscore only |

---

## Outbound request security

### TLS verification

The `http.Client` used for proxied requests (`handlers.go`) and for Token API login calls (`auth.go`) uses the default Go HTTP client configuration. This means:

- TLS certificates are verified against the system root CA store.
- Self-signed certificates will cause request failures.
- There is no `InsecureSkipVerify` flag exposed in the UI.

**Note:** The curl parser recognises and silently ignores the `-k` / `--insecure` flag for compatibility with pasted curl commands, but the proxied request itself still enforces TLS verification.

### CORS bypass (by design)

All outbound requests are proxied through the local Go server. The WKWebView does not make cross-origin requests directly to external APIs. This means CORS policies on external APIs do not apply — all requests appear to originate from `localhost`. This is the intended behaviour and is documented in the README.

**Risk:** A compromised web page loaded in a separate browser tab cannot call `localhost:8080` due to browser same-origin policies. However, native code on the local machine can.

---

## Local server exposure

The Gin server listens on `localhost:8080` (loopback only) with no authentication, no CSRF protection, and no origin checking.

### What a local attacker can do

Any process running as the same user (or any user, since the port is on localhost) can:

- Call `POST /send` to proxy HTTP requests through the app, using any configured auth profile, without knowing the token value.
- Call `GET /variables/map` to retrieve all variable values (including secrets stored in variables) as a JSON object.
- Call `GET /auth-profiles` to retrieve auth profile metadata (login URLs, token paths, header names) but **not** the cached token directly (only the HTML rendering of `auth_profiles.html` is returned).
- Call `GET /history/export` to download the full request history including URLs, headers, bodies, and auth profile IDs.

### Severity assessment

| Risk | Severity | Notes |
|---|---|---|
| Token exfiltration via `GET /variables/map` | Medium | Tokens stored as variables are directly readable |
| Proxy abuse via `POST /send` | Medium | Attacker can make authenticated requests on the user's behalf |
| History exfiltration | Low–Medium | May include sensitive URLs and request bodies |
| Auth profile metadata leak | Low | Login URL and config visible, not the token itself |

### Mitigations

- The server binds to `127.0.0.1` implicitly (`:8080` on Go's default listener). Network-level access from other machines is not possible.
- Standard macOS user-account isolation prevents other users' processes from reaching the port.
- The port is only open while the app is running.

**Recommendation:** Do not leave the app running unattended in environments where other untrusted user-level processes may execute (e.g. a shared developer workstation).

---

## XSS — response body rendering

Response bodies are injected into the template as `{{ .Body }}` inside a `<pre>` tag. Go's `html/template` package **automatically HTML-escapes** all template variables, so `<script>` tags in a JSON response body are rendered as `&lt;script&gt;` — they are not executed.

**Severity:** Low. HTML escaping is applied uniformly by the template engine.

**Note:** The response body preview is limited to 50,000 characters. This prevents very large crafted payloads from degrading rendering performance.

---

## Hardcoded secrets

A scan of all Go source files and templates found:

- **Zero** hardcoded credentials, API keys, or tokens in source code.
- The only hardcoded string values are configuration defaults: port `8080`, timeout `30s`, history cap `100`, display size limits `1400×900`.

Data files (`history.json`, `variables.json`, `auth_profiles.json`, `collections.json`) may contain user secrets at runtime but are excluded from version control via `.gitignore`.

---

## Dependency supply chain

Dependencies are pinned via `go.sum`. Notable dependencies and their security properties:

| Dependency | Version | Notes |
|---|---|---|
| `github.com/gin-gonic/gin` | v1.12.0 | Web framework; well-maintained |
| `github.com/webview/webview_go` | v0.0.0-20240831 | WKWebView bridge; macOS system component |
| `github.com/quic-go/quic-go` | v0.59.0 | HTTP/3 support pulled in transitively; not used directly |
| `go.mongodb.org/mongo-driver/v2` | v2.5.0 | Pulled in transitively; not used directly |

Run `go list -m all` to enumerate all transitive dependencies. Run `govulncheck ./...` (install via `go install golang.org/x/vuln/cmd/govulncheck@latest`) to check for known vulnerabilities.

---

## Recommendations summary

| Priority | Recommendation |
|---|---|
| High | Avoid storing long-lived production API keys in Variables; prefer short-lived tokens via Token API auth profiles |
| Medium | Add an optional startup PIN or macOS Keychain integration for `auth_profiles.json` token storage |
| Medium | Bind the local server to `127.0.0.1` explicitly (rather than relying on OS default) and add a random port allocation option |
| Low | Add `govulncheck` to the build process |
| Low | Add a "Clear all data" option in the UI to wipe sensitive data without manual file deletion |
