# Web UI Slice 1: Skeleton and Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A deployable binary that serves an embedded Svelte app from `/` with a strict Content-Security-Policy, shows a login screen, and after login shows the person's username.

**Architecture:** The Svelte project lives in `web/`; Vite builds it to `web/dist`, which the tiny Go package `web` embeds. `cmd/hermes-chat` hands that file system to `app` through `app.Options`, and two public routes serve it: `GET /` (the entry page, never cached) and `GET /assets/*filepath` (hashed files, cached for a year). The browser side is a fetch wrapper, one session state module, and two screens.

**Tech Stack:** Go 1.26, gin; Svelte 5, TypeScript 6, Vite 8; Vitest 5 with `@testing-library/svelte` on jsdom; `just`.

**Spec:** `docs/superpowers/specs/2026-10-12-web-ui-design.md` (slice 1 of section 8; sections 3, 6, 7 and 9 apply). The server's conventions are in `AGENTS.md`.

## Global Constraints

- `go build ./...`, `go vet ./...` and `go test ./...` pass on a fresh clone with no Node installed and no UI build. No Go test may require `web/dist/index.html` to exist; a test that inspects the built output skips when it is absent.
- The response header is exactly `Content-Security-Policy: default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. No inline `<script>`, no `style="…"` attribute anywhere in a component, no `{@html}`. The script rules are never relaxed.
- The browser makes no request to another origin: no CDN, no web fonts. Font stack is `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`.
- Every npm dependency is pinned to an exact version (no `^`) and installed with `npm ci` from the committed `web/package-lock.json`.
- **TypeScript stays on 6.x.** `svelte-check` 4.7.6 refuses to start under TypeScript 7 (checked 2026-10-12).
- No change to the JSON API. If one seems needed, stop and raise it with Kai.
- Layering: `app` receives an `fs.FS`; it never imports `web`. Only `cmd/hermes-chat` imports `web`.
- Never run `gofmt -w` on a directory in this repository; format only the files you created or edited.
- No hand-holding copy (spec section 2): no helper text, no tooltips, no empty-state coaching.
- Routing, when later slices need it, is hash-based. This slice has no router: the screen follows the session state.

## Decisions this plan makes (spec section 9, "left to the builder")

| Question | Decision |
|---|---|
| How `go build` finds `web/dist` before a UI build | `web/dist/.gitkeep` is committed and everything else in `web/dist` is ignored. The embed pattern is `all:dist`: plain `dist` fails on a directory holding only a dotfile, and skips Vite chunks whose names start with `_`. |
| Keeping `.gitkeep` through a Vite build | Vite empties `dist` on every build, then copies `web/public/` into it. A committed `web/public/.gitkeep` puts the file back, so the tree stays clean. (Verified.) |
| Go tools walking `web/node_modules` | `ignore ./web/node_modules` in `go.mod` (Go 1.25+). |
| The UI in `just` | `just build` depends on `web-build`, so a deploy binary always carries the current UI. `just check` gains `web-check` and therefore needs Node; `go test ./...` alone does not. There is no CI to change. |
| Component tests | Vitest + `@testing-library/svelte` + jsdom. |
| Inline styles | Avoided; the policy is not relaxed. |
| An unbuilt binary | `GET /` answers 404 `not_found`, and `serve` logs one warning at start. |
| Compression | Not in this slice. The whole app is about 13 kB gzipped; revisit in slice 6 if it matters. |

A **Log out** control is included although the spec's slice text does not name it: without it the login screen cannot be reached again to check it.

## Review Focus

1. **The server cannot be reached** when the page loads or on submit (phone leaving wifi, server restarting during an upgrade). Expected: a visible message and a way to retry, never a blank page or a stuck "loading". Tests in Tasks 4 and 5.
2. **An error response that is not the API's JSON** (a proxy's HTML 502, a truncated body). Expected: a plain fallback message, not a thrown `SyntaxError`. Test in Task 4.
3. **Submitting the login form twice** (double tap, Enter held). Expected: one request; the button is disabled while it is pending. Test in Task 5.
4. **An asset path that is not a file**: `/assets/`, a hash from before an upgrade, `/assets/../index.html`. Expected: 404, never a directory listing, never the entry page, and never the one-year cache header. Tests in Task 2.
5. **Password managers and phone keyboards.** Expected: the fields carry `autocomplete="username"` / `"current-password"`, and the username field does not capitalize or autocorrect. Test in Task 5.

## File Structure

```
go.mod                           + ignore ./web/node_modules
.gitignore                       + web/node_modules/, web/dist/*, !web/dist/.gitkeep
justfile                         + web-install, web-build, web-check, web-dev; build and check depend on them
web/
  embed.go                       package web: Dist() fs.FS
  embed_test.go                  Dist is rooted at dist; built entry page has no inline script
  dist/.gitkeep                  committed placeholder
  public/.gitkeep                copied into dist by every build
  package.json, package-lock.json, vite.config.ts, svelte.config.js, tsconfig.json, index.html
  src/
    main.ts                      mounts App
    app.css                      colour tokens, light and dark, base rules
    test-setup.ts                jest-dom matchers
    lib/api.ts                   request(), ApiError, login/logout/me, onUnauthenticated
    lib/api.test.ts
    lib/session.svelte.ts        session state; start/signIn/signOut
    lib/session.test.ts
    App.svelte                   picks the screen from session.status
    App.test.ts
    Login.svelte, Login.test.ts
    Home.svelte, Home.test.ts
cmd/hermes-chat/
  internal/app/static.go         Index, Asset, the security headers
  internal/app/static_test.go
  internal/app/app.go            Controller gains Index and Asset; Options gains UI
  routes.go, routes_test.go      two public routes
  wires.go, serve.go             pass web.Dist(); warn when unbuilt
docs/openapi.yaml, README.md, AGENTS.md, docs/deploy.md
```

---

### Task 1: The `web` package and its placeholder

**Files:**
- Create: `web/embed.go`, `web/embed_test.go`, `web/dist/.gitkeep`, `web/public/.gitkeep`
- Modify: `go.mod`, `.gitignore`

**Interfaces:**
- Produces: `web.Dist() fs.FS`, rooted so that the entry page is `index.html` and hashed files are under `assets/`. Before a UI build it holds only `.gitkeep`.

- [ ] **Step 1: Write the failing test** in `web/embed_test.go`

```go
package web

func TestDist_IsRootedAtTheBuildDirectory(t *testing.T) {
	_, err := fs.Stat(Dist(), ".gitkeep")
	require.NoError(t, err)
	_, err = fs.Stat(Dist(), "dist")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
```

- [ ] **Step 2: Run it.** `go test ./web/` — FAIL, `undefined: Dist`.

- [ ] **Step 3: Implement.** Create the two empty `.gitkeep` files. `web/embed.go` is package `web` with a package comment saying it holds the built web UI, is a leaf, and is imported only by `cmd/hermes-chat`; `//go:embed all:dist` on an `embed.FS`; `Dist()` returns `fs.Sub` of it at `dist`, panicking on the error (it cannot fail for a fixed valid path). Add to `go.mod`, after the `go` line:

```
ignore ./web/node_modules
```

Add to `.gitignore`:

```
web/node_modules/
web/dist/*
!web/dist/.gitkeep
```

- [ ] **Step 4: Run.** `go test ./web/ && go vet ./... && go build ./...` — all pass.

- [ ] **Step 5: Commit.** `feat(web): an embed package that builds before the UI does`

---

### Task 2: Serve the UI with the security header and caching

**Files:**
- Create: `cmd/hermes-chat/internal/app/static.go`, `cmd/hermes-chat/internal/app/static_test.go`
- Modify: `cmd/hermes-chat/internal/app/app.go` (`Controller`, `Options`), `cmd/hermes-chat/routes.go`, `cmd/hermes-chat/routes_test.go`, `cmd/hermes-chat/wires.go`, `cmd/hermes-chat/serve.go`, `docs/openapi.yaml`, `README.md`, `AGENTS.md`

**Interfaces:**
- Consumes: `web.Dist() fs.FS`.
- Produces: `app.Options.UI fs.FS` (nil means no UI); `Controller.Index(c *gin.Context)`; `Controller.Asset(c *gin.Context)`, which reads the gin param `filepath`; routes `GET /` and `GET /assets/*filepath`.

Behaviour, all of it pinned by the tests below:

| Request | Response |
|---|---|
| `GET /`, `index.html` present | 200, `Content-Type: text/html; charset=utf-8`, `Cache-Control: no-cache`, the three security headers |
| `GET /`, `UI` nil or no `index.html` | 404 `not_found` through `writeError(c, bus.ErrNotFound)` |
| `GET /assets/<file>` that exists | 200, type from the extension, `Cache-Control: public, max-age=31536000, immutable`, the three security headers |
| `GET /assets/<anything else>` | 404 `not_found`, no `Cache-Control` |

The three security headers: the `Content-Security-Policy` from Global Constraints, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`.

- [ ] **Step 1: Write the failing tests** in `static_test.go` (package `app_test`). Build the engine directly so the UI can be injected:

```go
var testUI = fstest.MapFS{
	"index.html":        {Data: []byte("<!doctype html><title>t</title>")},
	"assets/app-abc.js": {Data: []byte("console.log(1)")},
	"assets/app-abc.css": {Data: []byte("body{}")},
}

func uiEngine(t *testing.T, ui fs.FS) *gin.Engine {
	gin.SetMode(gin.TestMode)
	ctrl := app.NewController(realSvc(t), app.Options{UI: ui})
	e := gin.New()
	e.GET("/", ctrl.Index)
	e.GET("/assets/*filepath", ctrl.Asset)
	return e
}

const wantCSP = "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
```

Tests, each using the existing `do(e, method, target)` helper:

- `TestIndex_ServesTheEntryPage`: 200; body equals the map's bytes; `Content-Type` is `text/html; charset=utf-8`; `Cache-Control` is `no-cache`; `Content-Security-Policy` equals `wantCSP`; `X-Content-Type-Options` is `nosniff`; `Referrer-Policy` is `no-referrer`. It must not be a 301 (see step 3).
- `TestIndex_NotBuilt`: table over `nil` and `fstest.MapFS{".gitkeep": {}}`; 404 and the body's `error.code` is `not_found`.
- `TestAsset_ServesHashedFilesForAYear`: table over the `.js` and `.css` entries; 200; body matches; `Content-Type` starts with `text/javascript` / `text/css`; `Cache-Control` is `public, max-age=31536000, immutable`; CSP equals `wantCSP`.
- `TestAsset_NotAFile` (Review Focus 4): table over `/assets/`, `/assets/missing-123.js`, `/assets/../index.html`, `/assets/%2e%2e/index.html`, and `/assets/app-abc.js/`; each is 404 with `Cache-Control` empty and a body that does not contain `<title>`. Run the same table with `UI: nil`.

Add to `routes_test.go`:

- In `TestRoutes_Table`, add `"GET /"` and `"GET /assets/*filepath"` to the expected list in sorted position.
- In `TestRoutes_MatchTheOpenAPISpec`, also rewrite a wildcard: `*filepath` becomes `{filepath}` (extend the existing regexp replacement to `[:*](\w+)`).
- `TestRoutes_ServeTheWebUI`: call `routes` with `app.Options{UI: testUI}` (declare a small map in this file); `GET /` is 200 with the CSP header and an `X-Request-ID`; `GET /api/me` is still 401; `GET /nope` is 404.

- [ ] **Step 2: Run.** `go test ./cmd/hermes-chat/...` — FAIL: `Options` has no field `UI`, `Controller` has no `Index`.

- [ ] **Step 3: Implement.**

`app.go`: add `Index` and `Asset` to `Controller`; add `UI fs.FS // the built web UI; nil serves none` to `Options`.

`static.go`:
- `const contentSecurityPolicy` with the exact value, and a doc comment saying it is the whole policy and that script rules are not to be relaxed.
- `func uiHeaders(c *gin.Context)` sets the three security headers.
- `Index`: read `index.html` with `fs.ReadFile` and write it with `c.Data`. **Do not use `c.FileFromFS` or `http.ServeFileFS` here**: Go's file server answers any path ending in `index.html` with a 301 to `./`.
- `Asset`: the param arrives with its leading slash (`/app-abc.js`). Require it to be already clean (`path.Clean(p) == p`, which refuses `..`, doubled slashes and a trailing slash), join it under `assets`, and require `fs.Stat` to succeed on a regular file (not a directory) before writing any header; otherwise `writeError(c, bus.ErrNotFound)`. The check must come first: `http.ServeFileFS` answers a `..` path with 400, not 404. Then set the headers and serve with `http.ServeFileFS`, which supplies the content type and handles range and `HEAD` correctly.

`routes.go`, in the public block, after `/healthz`:

```go
	e.GET("/", ctrl.Index)
	e.GET("/assets/*filepath", ctrl.Asset)
```

`wires.go`: `app.Options{CookieSecure: cfg.CookieSecure, UI: web.Dist()}`.

`serve.go`: after `wireUp`, if `fs.Stat(web.Dist(), "index.html")` fails, `log.Warn("web UI not built; / will answer 404", "fix", "just build")`.

- [ ] **Step 4: Document.**

`docs/openapi.yaml`: add a tag `web UI` (`description: The embedded browser app. Static files, no credentials.`) and two paths in the style of `/healthz`, both `security: []`:
- `/` `get`, `operationId: index`: 200 `text/html` (describe the cache and security headers in the description), 404 `Error` when the binary was built without the UI.
- `/assets/{filepath}` `get`, `operationId: asset`, path parameter `filepath` (string): 200 with the one-year cache header, 404 `Error`.

`README.md`: a new `## Web UI` section before `## HTTP API` stating the two routes, the exact policy string, the two cache rules, and that an unbuilt binary answers 404 on `/`.

`AGENTS.md`: add `internal/app/static.go` to the `internal/app` bullet; add a `web` bullet under shared leaves ("the built web UI behind `//go:embed all:dist`; imported only by `cmd/hermes-chat`, which passes it to `app` as an `fs.FS`"); add `web/` to Key Directories.

- [ ] **Step 5: Run.** `go test ./... && go vet ./...` — pass, including `TestRoutes_MatchTheOpenAPISpec` and `TestLayering_ThisRepository`.

- [ ] **Step 6: Commit.** `feat(app): serve the embedded web UI with a strict content security policy`

---

### Task 3: The Svelte project and its build

**Files:**
- Create: `web/package.json`, `web/package-lock.json`, `web/vite.config.ts`, `web/svelte.config.js`, `web/tsconfig.json`, `web/index.html`, `web/src/main.ts`, `web/src/app.css`, `web/src/test-setup.ts`, `web/src/App.svelte`, `web/src/App.test.ts`
- Modify: `web/embed_test.go`, `justfile`, `README.md`, `AGENTS.md`, `docs/deploy.md`

**Interfaces:**
- Produces: `npm run build` (writes `web/dist/index.html` and `web/dist/assets/*`), `npm test` (`vitest run`), `npm run check` (`svelte-check`), `npm run dev`; recipes `just web-install`, `web-build`, `web-check`, `web-dev`. CSS custom properties from `app.css` for later tasks: `--page --ink --ink2 --aub --aub-hi --aub-lo --aub-line --on-aub --on-aub2 --paper --paper2 --line --accent --err`.

- [ ] **Step 1: Write the failing Go test** in `web/embed_test.go`

`TestDist_EntryPageHasNoInlineScriptOrStyle`: read `index.html` from `Dist()`; `t.Skip("web UI not built")` when it is absent. Otherwise assert that every `<script` tag in it has a `src` attribute, that it contains no `<style` and no ` style=`, and that every `src=` and `href=` value starts with `/assets/`.

Run `go test ./web/ -v` — the new test SKIPs. It turns into a real check in step 6.

- [ ] **Step 2: Create the project.** `web/package.json`:

```json
{
  "name": "hermes-chat-web",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "test": "vitest run",
    "check": "svelte-check --tsconfig ./tsconfig.json"
  },
  "devDependencies": {
    "@sveltejs/vite-plugin-svelte": "7.3.1",
    "@testing-library/jest-dom": "7.0.1",
    "@testing-library/svelte": "5.4.2",
    "@testing-library/user-event": "14.6.7",
    "jsdom": "30.1.2",
    "svelte": "5.57.2",
    "svelte-check": "4.7.6",
    "typescript": "6.0.3",
    "vite": "8.3.4",
    "vitest": "5.0.4"
  }
}
```

Run `npm install` in `web/` once to produce `package-lock.json`; commit the lockfile. This set was built, tested and type-checked together on 2026-10-12.

`web/vite.config.ts`:

```ts
import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';

export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  // No changeOrigin: the server's same-origin check compares Origin with Host,
  // and both must stay localhost:5173.
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
  test: { environment: 'jsdom', setupFiles: ['./src/test-setup.ts'] },
});
```

`web/svelte.config.js` exports `{ preprocess: vitePreprocess() }`. `web/tsconfig.json`: `strict`, `target` and `lib` ES2022 plus `DOM` and `DOM.Iterable`, `module: ESNext`, `moduleResolution: bundler`, `verbatimModuleSyntax`, `isolatedModules`, `noEmit`, `skipLibCheck`, `types: ["svelte", "vite/client", "vitest/globals", "@testing-library/jest-dom"]` (`vite/client` is what lets `import './app.css'` type-check), `include: ["src/**/*.ts", "src/**/*.svelte"]`. `web/src/test-setup.ts` is the single line `import '@testing-library/jest-dom/vitest';`.

`web/index.html`: `lang="en"`, charset, `<meta name="viewport" content="width=device-width, initial-scale=1">`, `<meta name="color-scheme" content="light dark">`, `<title>Hermes Chat</title>`, `<div id="app"></div>`, and `<script type="module" src="/src/main.ts"></script>`. Nothing inline.

`web/src/main.ts` imports `./app.css` and mounts `App` on `#app` with Svelte 5's `mount`.

`web/src/app.css`: the custom properties listed under Interfaces on `:root`, with a `@media (prefers-color-scheme: dark)` override, using these values from the approved mockup:

| Token | Light | Dark |
|---|---|---|
| `--page` | `#efeaf0` | `#120c14` |
| `--ink` / `--ink2` | `#221a24` / `#6f6373` | `#efe6f1` / `#a493a8` |
| `--aub` / `--aub-hi` / `--aub-lo` / `--aub-line` | `#3b1b42` / `#4c2555` / `#2c1232` / `#5a3463` | `#2a1230` / `#3a1b42` / `#1f0c24` / `#47294f` |
| `--on-aub` / `--on-aub2` | `#f3e9f5` / `#bfa6c5` | same |
| `--paper` / `--paper2` / `--line` | `#fdfbfd` / `#f4eef5` / `#e2d8e4` | `#1b131e` / `#241a28` / `#382a3d` |
| `--accent` | `#7a2f8a` | `#c98ad6` |
| `--err` | `#d2473c` | same |

Plus: `box-sizing: border-box` everywhere, `body` with no margin, the font stack from Global Constraints, `background: var(--page)`, `color: var(--ink)`; form controls inherit the font; a 16px minimum font size on inputs (smaller makes iOS zoom the page on focus).

- [ ] **Step 3: Write the failing component test** `web/src/App.test.ts`: render `App`; expect `screen.getByRole('heading', { name: 'Hermes Chat' })`. Run `npm test` in `web/` — FAIL, `App.svelte` does not exist.

- [ ] **Step 4: Implement** `web/src/App.svelte` as a placeholder: an `<h1>Hermes Chat</h1>` on the aubergine header colours. Task 5 replaces it. Run `npm test` and `npm run check` — both pass with 0 errors.

- [ ] **Step 5: Wire `just`.** Add recipes and change two:

```
web-install:
    cd web && npm ci

web-build: web-install
    cd web && npm run build

web-check: web-install
    cd web && npm run check && npm test

web-dev:
    cd web && npm run dev

build goos="linux" goarch="amd64": web-build
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -o bin/hermes-chat-{{goos}}-{{goarch}} ./cmd/hermes-chat

check: check-generated vet lint-scripts web-check test
```

- [ ] **Step 6: Verify the plumbing.** Each of these must hold:

| Command | Expected |
|---|---|
| `just web-build` | writes `web/dist/index.html`, `web/dist/assets/index-<hash>.js` and `.css` |
| `git status --porcelain` | lists only this task's new and modified source files; nothing under `web/dist` or `web/node_modules`, and `web/dist/.gitkeep` is not shown as deleted |
| `go test ./web/ -v` | `TestDist_EntryPageHasNoInlineScriptOrStyle` now PASSes rather than skips |
| `go list ./... \| grep -c node_modules` | `0` |
| `just check` | passes |
| `d=$(mktemp -d) && git stash -u -q && git clone -q . "$d" && git stash pop -q && (cd "$d" && go build ./... && go vet ./... && go test ./web/ ./cmd/hermes-chat/internal/app/)` | passes: a clone with no `node_modules` and no build still compiles and tests. (Commit first and clone `HEAD` instead if stashing is awkward.) |

- [ ] **Step 7: Document.**

`README.md`: list the four new recipes under Recipes, and say that `build` now builds the UI first and `check` now needs Node; extend the `## Web UI` section with the layout (`web/src` source, `web/dist` build output, never committed), the exact-pin and TypeScript 6 rules, and the dev loop:

```
HERMES_CHAT_DB_PATH=dev.db HERMES_CHAT_TLS_MODE=off HERMES_CHAT_COOKIE_SECURE=false go run ./cmd/hermes-chat serve
just web-dev        # http://localhost:5173, /api proxied to :8080
```

`AGENTS.md`: the new recipes under Development Commands; under Code Conventions a **Web UI** bullet (no inline script or `style` attributes, no `{@html}`, exact pins, TypeScript 6, `go test ./...` must not need a UI build); the web tier under Testing & QA.

`docs/deploy.md`: section 1 gains Node and npm on the build machine at the README's versions; sections 5 and 8 note that `just build` builds the UI first; section 6 gains a check:

```
curl -sI https://<domain>/ | grep -i -E 'content-security-policy|cache-control'
```

Worked: the policy string above and `Cache-Control: no-cache`.

- [ ] **Step 8: Commit.** `feat(web): a Svelte project, built by just and embedded in the binary`

---

### Task 4: The API client and the session state

**Files:**
- Create: `web/src/lib/api.ts`, `web/src/lib/api.test.ts`, `web/src/lib/session.svelte.ts`, `web/src/lib/session.test.ts`

**Interfaces:**
- Consumes: the server's `POST /api/login` (`{username, password}` → `{id, username}`), `POST /api/logout` (204), `GET /api/me` (`{id, username}`), and the error shape `{"error":{"code","message"}}` with `Retry-After` in seconds on 429.
- Produces, from `api.ts`:

```ts
export type Me = { id: string; username: string };

export class ApiError extends Error {
  readonly status: number;            // 0 when no response arrived
  readonly code: string;              // the server's code, or 'network', or 'http'
  readonly retryAfter: number | null; // seconds, from Retry-After
}

export function request<T>(method: string, path: string, body?: unknown): Promise<T>;
export function onUnauthenticated(handler: () => void): void; // one handler; a later call replaces it
export function login(username: string, password: string): Promise<Me>;
export function logout(): Promise<void>;
export function me(): Promise<Me>;
```

- Produces, from `session.svelte.ts`:

```ts
export const session: { status: 'loading' | 'in' | 'out' | 'error'; me: Me | null; error: string };
export function start(): Promise<void>;                                // never rejects
export function signIn(username: string, password: string): Promise<void>; // rejects with ApiError
export function signOut(): Promise<void>;                              // never rejects
```

Rules for `request`:
- JSON body and `Content-Type: application/json` only when `body` is given; `Accept: application/json` always. Paths are relative, so the cookie and the `Origin` header go with it.
- 204, or an empty 2xx body, resolves `undefined`.
- A non-2xx response rejects with `ApiError` built from the body's `error.code` and `error.message`. If the body is not that shape, `code` is `'http'` and `message` is `` `server error (${status})` ``.
- A rejected `fetch` rejects with `ApiError` status 0, code `'network'`, message `'cannot reach the server'`.
- On status 401 with a code other than `'invalid_credentials'`, call the unauthenticated handler before rejecting. (A wrong password is also a 401 and must not count as a lost session.)

Rules for the session:
- The module registers `onUnauthenticated` once, at import: status `'out'`, `me` null.
- `start`: status `'loading'`; `me()` resolves → `'in'` with `me` set; a 401 → `'out'`; anything else → `'error'` with `error` set to the `ApiError`'s message.
- `signIn`: on success `'in'` with `me` set; on failure it leaves the state alone and rethrows.
- `signOut`: calls `logout()`; whatever the outcome, status `'out'` and `me` null.

- [ ] **Step 1: Write the failing tests.** Stub `fetch` with `vi.stubGlobal('fetch', vi.fn())` and restore in `afterEach`; build responses with `new Response(body, { status, headers })`.

`api.test.ts`:
- `login posts JSON to /api/login`: the fetch call is `('/api/login', { method: 'POST', … })` with body `{"username":"kai","password":"pw"}` and the JSON content type; resolves `{ id: '01J', username: 'kai' }`.
- `me sends no body and no content type`.
- `logout resolves undefined on 204`.
- `a server error becomes an ApiError`: 401 `{"error":{"code":"invalid_credentials","message":"wrong username or password"}}` → status 401, that code, that message, `retryAfter` null.
- `Retry-After is parsed`: 429 `throttled` with `Retry-After: 42` → `retryAfter === 42`.
- `a body that is not the error shape gets a fallback` (Review Focus 2): 502 with `<html>Bad Gateway</html>` → code `'http'`, message `'server error (502)'`; also 500 with an empty body.
- `a failed fetch is a network error` (Review Focus 1): `fetch` rejects with `TypeError` → status 0, code `'network'`, message `'cannot reach the server'`.
- `an expired session calls the handler`: 401 `unauthenticated` → handler called once, and the promise still rejects.
- `a wrong password does not call the handler`.

`session.test.ts` (mock `./api` functions with `vi.mock`, keeping the real `ApiError` and `onUnauthenticated`; set `session.status = 'loading'` in `beforeEach`):
- `start with a live session`: `'in'`, `me.username === 'kai'`.
- `start with no session`: `me()` rejects 401 → `'out'`.
- `start when the server is unreachable` (Review Focus 1): `'error'`, `error === 'cannot reach the server'`, and `start()` resolved rather than threw.
- `signIn success`, and `signIn failure rethrows and leaves status 'out'`.
- `signOut ends the session even when logout fails`: `logout` rejects with a network error → `'out'`.
- `a 401 on any later request signs the person out`: with status `'in'`, drive a real `request('GET', '/api/me')` against a stubbed 401 `unauthenticated` → `'out'`. (This one uses the real `api` module; put it in its own `describe` with `vi.doUnmock` or a separate test file.)

- [ ] **Step 2: Run.** `cd web && npm test` — FAIL, modules not found.

- [ ] **Step 3: Implement** both modules to the interfaces and rules above. `session` is a single exported `$state` object (the file must keep the `.svelte.ts` suffix for the rune to compile).

- [ ] **Step 4: Run.** `npm test && npm run check` — pass, 0 errors.

- [ ] **Step 5: Commit.** `feat(web): an API client and the session state`

---

### Task 5: The login and signed-in screens

**Files:**
- Create: `web/src/Login.svelte`, `web/src/Login.test.ts`, `web/src/Home.svelte`, `web/src/Home.test.ts`
- Modify: `web/src/App.svelte`, `web/src/App.test.ts`, `web/src/app.css`

**Interfaces:**
- Consumes: `session`, `start`, `signIn`, `signOut` from `./lib/session.svelte`; `ApiError` from `./lib/api`; the tokens in `app.css`.
- Produces: `App.svelte` calls `start()` once when mounted and renders by `session.status`. Later slices replace `Home.svelte` with the room list.

Screens and exact copy:

| `session.status` | Shows |
|---|---|
| `loading` | the header only (`Hermes Chat`); no spinner, no text |
| `out` | `Login` |
| `in` | `Home` |
| `error` | the header, `session.error` in an element with `role="alert"`, and a **Retry** button that calls `start()` |

**Login:** one header reading `Hermes Chat`; a form with a **Username** field (`type="text"`, `name="username"`, `autocomplete="username"`, `autocapitalize="none"`, `autocorrect="off"`, `spellcheck="false"`, `required`), a **Password** field (`type="password"`, `name="password"`, `autocomplete="current-password"`, `required`), and a **Log in** submit button. Labels are real `<label>` elements. While a submit is pending the button is disabled and a second submit does nothing. On failure the `ApiError`'s message is shown in a `role="alert"` element; when `retryAfter` is not null the text is the message followed by `` ` Retry in ${retryAfter} s.` ``. The alert clears on the next submit. The password field is emptied after a failure; the username is kept. Nothing else is on the screen.

**Home:** the header `Hermes Chat` with a **Log out** button in it, and the username in the body. Nothing else.

Layout: one centred column, `max-width: 26rem` for the login form; the header uses `--aub` with `--on-aub` text; the form sits on `--paper`. Colours and spacing are not final (spec section 2), so keep the CSS short. No `style` attributes: use classes and component `<style>` blocks, which Vite extracts to the stylesheet.

- [ ] **Step 1: Write the failing tests.** Mock `./lib/session.svelte` in `Login.test.ts` and `Home.test.ts` (a plain object for `session`, `vi.fn()` for the functions); use `userEvent`.

`Login.test.ts`:
- `submits the username and password`: type `kai` / `pw`, click **Log in** → `signIn` called once with `('kai', 'pw')`.
- `Enter in the password field submits`.
- `shows the server's message on a wrong password`: `signIn` rejects with a 401 `ApiError` → `getByRole('alert')` has text `wrong username or password`; the password input is empty; the username input still reads `kai`.
- `shows the throttling message with the wait`: rejects 429 with message `too many attempts; try again later` and `retryAfter` 42 → alert text is `too many attempts; try again later Retry in 42 s.`
- `shows a network failure`: rejects with the status-0 error → alert reads `cannot reach the server` (Review Focus 1).
- `does not submit twice` (Review Focus 3): `signIn` returns a promise that is not yet resolved; click **Log in** twice and press Enter → called once; the button is disabled; after the promise rejects the button is enabled again.
- `fields suit password managers and phones` (Review Focus 5): assert the `autocomplete`, `autocapitalize`, `type` and `required` attributes named above.

`Home.test.ts`:
- `shows the username`: `session.me = { id: '01J', username: 'kai' }` → `getByText('kai')`.
- `Log out calls signOut`.

`App.test.ts` (replace the placeholder test; mock `./lib/api` so the real session module runs):
- `no session shows the login form`: `me` rejects 401 → `findByLabelText('Username')`.
- `a live session shows the username`: `me` resolves → `findByText('kai')`, and no `Password` field.
- `logging in moves to the signed-in screen`: start signed out, `login` resolves → after submit, `findByRole('button', { name: 'Log out' })`.
- `logging out returns to the login form`.
- `an unreachable server shows the error and Retry` (Review Focus 1): `me` rejects with the network error → alert `cannot reach the server`; then make `me` resolve and click **Retry** → `findByText('kai')`.

- [ ] **Step 2: Run.** `cd web && npm test` — FAIL.

- [ ] **Step 3: Implement** the three components to the tables and copy above.

- [ ] **Step 4: Run.** `npm test && npm run check`, then `just check` from the repository root — all pass.

- [ ] **Step 5: Try it against a real server.** This is the check the unit tests cannot make: that the browser accepts the page under the policy.

```
just build "$(go env GOOS)" "$(go env GOARCH)"
export HERMES_CHAT_DB_PATH="$(mktemp -d)/t.db" HERMES_CHAT_TLS_MODE=off HERMES_CHAT_COOKIE_SECURE=false
bin/hermes-chat-* migrate
echo 'a-test-password' | bin/hermes-chat-* user add kai   # reads the password from standard input
bin/hermes-chat-* serve &
```

| Check | Expected |
|---|---|
| `curl -si localhost:8080/ \| head -20` | 200; the policy header exactly as in Global Constraints; `Cache-Control: no-cache`; `nosniff`; no startup warning in the server log |
| `curl -sI localhost:8080/assets/<the js file named in the page>` | 200; `Cache-Control: public, max-age=31536000, immutable`; `Content-Type: text/javascript; charset=utf-8` |
| `curl -s -o /dev/null -w '%{http_code}' localhost:8080/assets/` | `404` |
| `curl -si -c jar -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' -d '{"username":"kai","password":"…"}' localhost:8080/api/login` then `curl -s -b jar localhost:8080/api/me` | 200 and `{"id":…,"username":"kai"}` |
| In a browser at `http://localhost:8080/`, with the console open | the login form renders styled; a wrong password shows the message; a right one shows `kai`; a reload still shows `kai`; **Log out** returns to the form; the console shows **no** Content-Security-Policy violation and the network tab shows no request to another origin |

If no browser is available where this runs, say so in the handover instead of claiming the last row; Kai checks it after deploying.

- [ ] **Step 6: Commit.** `feat(web): login, and the signed-in screen`

---

## Handover

The slice ends deployed (spec section 8). Deploying is the operator's job and there is no `just deploy`: Kai builds with `just build linux <arch>` and follows `docs/deploy.md` section 8. After the upgrade, the checks are the `curl -sI` line added to section 6 and the browser row from Task 5 step 5, on a phone and on a desktop browser.

Slice 2 (the live room list) gets its own plan once this one is merged.
