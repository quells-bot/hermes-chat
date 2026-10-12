# Repository Guidelines

## Project Overview

Hermes Chat is a chat server: a single Go binary exposing a JSON/HTTP API with an SSE event stream, backed by SQLite. Humans authenticate with sessions (argon2id passwords); agents authenticate with bearer tokens. The product is documented in `README.md` (API contract, env vars, limits) and the specs under `docs/superpowers/specs/`; stages 1–7 of the architecture plan are implemented.

## Architecture & Data Flow

Module `github.com/quells-bot/hermes-chat`; the server lives under `cmd/hermes-chat`, reusable packages under the repository-root `internal/`, and the fake agent command under `cmd/fakeagent`:

- **`cmd/hermes-chat` (package main)** — server: dispatch (`main.go`), config (`config.go`), wiring (`wires.go`), HTTP bootstrap (`serve.go`, `routes.go`), logging (`logging.go`), layering check (`layering.go`).
- **`internal/cli`** — admin subcommands (`migrate`, `user`, `agent`) in `admin.go`/`admin_agent.go`, terminal password input (`password_input.go`); wires bus+storage itself (`openSvc`), builds no hub and no HTTP controller.
- **`internal/app`** — gin HTTP layer: handlers (`room.go`, `message.go`, `pause.go`, `auth.go`, `stream.go`, `prompt.go`, `note.go`, `attachment.go`), middleware (`middleware.go`), JSON bodies (`json.go`), bus-error→HTTP mapping (`errors.go`). Knows HTTP, nothing about SQL.
- **`internal/bus`** — business logic. `Service` interface (`bus.go`), sentinel errors (`ErrNotFound`, `ErrConflict`, …), auth/rate limiting (`session.go`, `throttle.go`, `password.go`), event fan-out `Hub` (`hub.go`), the off switch (`pause.go`, `halt.go`, `turncap.go`: room pause, stop everything, the turn cap), and the stage 6 features (`prompt.go`, `note.go`, `attachment.go`: prompts, the per-human note, uploads). Knows no HTTP or SQL.
- **`internal/storage`** — `Repository` interface over sqlc queries; two SQLite pools (`db.go`: 1 write connection with `_txlock=immediate`, ≥4 read connections, WAL); error translation (`mapErr`); migrations (`migrate.go`, goose as a library, embedded via `go:embed`); the stage 6 rows (`prompt.go`, `note.go`, `attachment.go`) and the attachment file store (`files.go`).
- **Shared leaves** at the repository root, not under `cmd/hermes-chat`: `internal/reqid` (ULID per request, slog handler), `internal/sse` (frame encode/decode), and `internal/fakeagent` (a client for the agent API, used by the e2e tests and by `cmd/fakeagent`). They import none of the layers, and the server does not import `fakeagent` outside tests; that is a convention stated in the package comments, not something the layering test checks.
- **`cmd/fakeagent`** — a small command wrapping `internal/fakeagent`: connects as one agent (`FAKEAGENT_URL`, `FAKEAGENT_TOKEN`), prints each event as a JSON line, echoes every delivery, and reconnects from its cursor. For trying a server by hand.

Request flow: gin route → app handler → `bus.Service` method → `storage.Repository` → sqlcgen → SQLite. Writes happen inside one transaction; `bus` writes the event row and rings the Hub in the same tx (`bus/event.go`). SSE handlers (`app/stream.go`) wake on the ring, then poll events after the subscription cursor (`storage.ListEventsForHuman`), so a stream never depends on in-memory state.

**Layering is enforced by a test, not convention** (`TestLayering_ThisRepository`): app may import bus but not storage; bus may import neither app, sqlcgen, gin, nor `net/http`; storage may import neither bus nor app nor gin; `cli`, like `main`, sits above the layers and imports bus and storage. Only `storage` may import `sqlcgen`. The map lives in `cmd/hermes-chat/layering.go`; the test parses the real import graph via `go list -json`.

## Key Directories

| Path | Purpose |
|---|---|
| `cmd/hermes-chat/` | server: dispatch, config, wiring, serve/routes, layering test |
| `cmd/fakeagent/` | fake agent command for trying a server by hand |
| `internal/` | shared leaves: `reqid`, `sse`, `fakeagent` |
| `cmd/hermes-chat/internal/cli/` | admin subcommands (migrate, user, agent) with their own wiring |
| `cmd/hermes-chat/internal/app/` | HTTP handlers, middleware, SSE endpoint, error mapping |
| `cmd/hermes-chat/internal/bus/` | Service, domain types, sessions, tokens, throttling, hub |
| `cmd/hermes-chat/internal/storage/` | Repository impl, DB pools, migrations |
|`cmd/hermes-chat/internal/storage/queries/`|sqlc query definitions (`activities`, `agents`, `attachments`, `deliveries`, `events`, `halt`, `humans`, `messages`, `notes`, `prompts`, `rooms`, `sessions`)|
| `cmd/hermes-chat/internal/storage/migrations/` | goose SQL migrations `NNNNN_name.sql` |
| `cmd/hermes-chat/internal/storage/sqlcgen/` | generated — never edit |
| `cmd/hermes-chat/internal/storage/storagetest/` | test-only helper; production code must not import it (layering test enforces) |
| `deploy/` | systemd unit, environment example, `hermes-chat-upgrade` and `hermes-chat-admin` scripts, upgrade test |
| `docs/deploy.md` | operator runbook: host setup, first start, upgrade, rollback |
| `docs/superpowers/` | architecture/product specs and stage plans — read before changing design |

## Development Commands

```
just check            # sqlc diff + go vet ./... + go test ./...  (run before handing over)
just generate         # sqlc generate after editing queries or migrations
just check-generated  # sqlc diff; fails if sqlcgen is stale
just vet
just test             # go test ./...
just build [goos] [goarch]  # CGO_ENABLED=0, default linux/amd64 → bin/hermes-chat-<goos>-<goarch>
go test ./...         # equivalent to just test
```

Locally: `go run ./cmd/hermes-chat serve` (requires `HERMES_CHAT_DB_PATH`; migrate first via `migrate`).

## Code Conventions & Common Patterns

- **Configuration**: envconfig struct in `config.go`, env prefix `HERMES_CHAT_` only; validation of debug addr/gin mode/proxies happens in `validateForServe`, not at load. `serve` opens the attachments directory (`wireUp` in `wires.go`). TLS is `HERMES_CHAT_TLS_MODE` (`autocert` by default, or `off`), `HERMES_CHAT_TLS_DOMAIN` (required for autocert), and `HERMES_CHAT_TLS_CACHE_DIR`; `cmd/hermes-chat/tls.go` builds the certificate manager and wraps the API listener.
- **Errors**: bus defines sentinels + `ValidationError` in `bus/bus.go`; never build HTTP responses in bus. `app/errors.go` has the single errorTable mapping them to status + `{"error":{"code","message"}}`. New error kinds go in both places, plus `README.md`'s table.
- **Validation** happens in bus, not handlers: trim, then reject with `ValidationError` carrying the user-visible message.
- **Transactions**: bus methods that must be atomic call `storage.Repository.InTx`; the event row is written in the same tx as the change, then the Hub is rung (`bus/event.go`).
- **IDs and time**: ULIDs from `ulid.New(t, entropy)`; IDs are identifiers, never secrets — tokens use 32 random bytes (`bus/token.go`). Time is injected via the `Clock` func so tests fix it; `now()` truncates to UTC milliseconds before storage.
- **Concurrency**: single write connection (`_txlock=immediate`); the Hub is a doorbell — ring buffered channels, one indexed poll per ring, batch 100; subscriptions accept humans and agents.
- **Logging**: one JSON line per request via `app/middleware.go` (RequestID + RequestLogger); `reqid` threads through slog.
- **The off switch** is one gate: `releaseHead` (`bus/delivery.go`) re-reads the room and the halt inside the transaction and releases nothing while `paused()` is true, and agent writes check the same function. Closing a room also expires its open prompts (`expireOpenPrompts`), and a prompt, a note suggestion, and an upload are agent writes that check `paused()`. A new way to deliver to or accept content from an agent must go through one of the two.
- **Migrations**: next numbered file in `migrations/`, `-- +goose Up`/`-- +goose Down`, every table `STRICT`. After adding one: `just generate`.
- **Queries**: add named queries under `storage/queries/*.sql`, then `just generate`; call them only from `storage`, wrapped as `Repository` methods.

## Important Files

- `cmd/hermes-chat/main.go` — CLI dispatch; exit 2 on usage without config
- `cmd/hermes-chat/wires.go` — `wireUp()`: storage → bus → app composition
- `cmd/hermes-chat/internal/cli/admin.go` — `Migrate`/`User`/`Agent` and the cli-owned `openSvc` wiring
- `cmd/hermes-chat/tls.go` — `certManager` and `apiListener`: autocert on the API listener, TLS-ALPN challenge on 443
- `deploy/hermes-chat-upgrade` — on-box upgrade: database copy, binary swap, health check, rollback; scripts read the env file without sourcing it, so values are unquoted
- `cmd/hermes-chat/layering.go` — layering rules; `cmd/hermes-chat/layering_test.go` enforces them
- `cmd/hermes-chat/internal/app/errors.go` — error code ↔ HTTP status table
- `cmd/hermes-chat/internal/bus/bus.go` — Service interface, sentinel errors, Service impl
- `cmd/hermes-chat/internal/storage/db.go` — pools and pragmas (WAL, busy_timeout 5s, foreign_keys on)
- `cmd/hermes-chat/internal/storage/migrate.go` — embeds and applies migrations
- `sqlc.yaml` — schema dir = migrations, queries dir, output package `sqlcgen`

## Runtime/Tooling Preferences

- Go 1.26 (toolchain 1.26.5); build with `CGO_ENABLED=0` — SQLite is `modernc.org/sqlite` (pure Go, no cgo).
- Tool versions in `README.md`: sqlc 1.31.1, just 1.45.0, Node 24.18.1, Python 3.14.4/uv 0.11.31, shellcheck 0.11.0, sqlite3 3.46.1.
- Dependencies are Go-only; there is no Node/Python runtime component of the server itself.
- `sqlcgen/` output is committed; `just check-generated` (sqlc diff) must stay clean.

## Testing & QA

`go test ./...` (~10s, no flags needed). Three tiers, plus the deploy script:

- **Unit** — the server's `internal/bus` and `internal/storage`, and the shared `internal/sse` and `internal/reqid`: storage tests get a migrated SQLite via `storage/storagetest.NewRepo`; `bus` takes a fixed `Clock` and `export_test.go` exposes internals (throttle state, argon limiter) for testing.
- **HTTP** — `internal/app` (package `app_test`): httptest against a real bus; helpers `newEngine`, `authEngine`, `newChat`.
- **E2E** — `cmd/hermes-chat/serve_test.go` spawns the real server (CLI + HTTP + SSE); `internal/cli` tests drive `Migrate`/`User`/`Agent` with a temp DB, and `cmd/hermes-chat/main_test.go` pins the dispatch entries.
- **Deploy script** — `go test ./deploy` runs `deploy/hermes-chat-upgrade` against stubbed `systemctl` and `curl`; `just lint-scripts` runs `shellcheck` on both scripts.

Conventions: testify assert/require; table tests for validation/error mapping; tests may import `export_test.go` aliases but never production code from another layer. Layering and route tests double as architecture checks (`TestLayering_ThisRepository`, route ordering). No coverage target is enforced; `just check` is the gate.
