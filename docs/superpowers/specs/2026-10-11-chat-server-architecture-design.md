# Hermes Chat server: architecture and build conventions

Date: 2026-10-11
Status: draft for approval
Companion to: `2026-10-11-hermes-chat-design.md` (the product spec). That
document says what the chat server does. This one says how its Go code is
organized and which libraries it is built from. Where the two disagree on a
build detail, this one wins; section 13 lists the edits made to the product
spec to keep them in line.

Scope: the Go chat server and its fake agent test client. `web/` and `plugin/`
appear only as places in the layout; their internals are not covered here.

Items marked *(Kai)* were decided by Kai. Items marked *(Claude's call)* were
not discussed in detail and can be changed if there is a reason.

## 1. Intent

Kai chose Go so he can read the server, reason about it, and catch the gotchas
himself. A fixed, familiar shape serves that: every request takes the same path
through the same three layers, every long-running thing is started and stopped
the same way, and every database access looks the same. Kai should be able to
open any file and know which layer he is in and what it is allowed to touch.

The shape is the one Kai already uses: the three-tier layout of the `catalog`
example in `github.com/quells-bot/ddb-sqlite`, with `github.com/quells/grace`
running the long-lived services. **Only the high-level organization is borrowed
from catalog** *(Kai)*: three layers, their direction of dependency, and
`routes.go` and `wires.go` beside `main.go`. Catalog's other choices (stdlib
`ServeMux`, DynamoDB, no transactions) do not carry over.

## 2. Goals, non-goals, anti-goals

### Goals

- `app` → `bus` → `storage`, with the dependency direction enforced by a test.
- One pattern for long-running services, so adding a background job later means
  writing one `Run(ctx) error` method and adding it to a list.
- The API and pprof served on separate ip:port pairs.
- Tests are cheap enough to run against the real layers below, every time.

### Non-goals

- A reusable framework. This is one service.
- Dependency-injection libraries, code generation beyond sqlc, or an ORM.
- Minimizing boilerplate. Mapping types by hand at each layer boundary is
  accepted *(Kai)*.

### Anti-goals

- **`app` reaching into `storage`**, directly or by passing storage types
  through `bus` untouched.
- **Business rules in SQL-shaped repo methods.** The bus decides; storage
  stores.
- **A second way of doing something the pattern already covers**: a goroutine
  started outside `grace.Run`, a database handle opened outside `wires.go`, an
  ID that is not a ULID (the event cursor excepted, 7.4).

## 3. Layout

Standard Go project layout *(Kai)*, one Go module at the repository root:

```
go.mod
justfile
sqlc.yaml
scripts/             shell scripts for any task too long for a just recipe
cmd/hermes-chat/
    main.go          subcommand dispatch; for serve: config, wireUp, grace.Run
    config.go        envconfig struct
    wires.go         builds storage → bus → app; collects clean-up funcs
    routes.go        every route and its middleware, in one place
    internal/
        cli/          admin subcommands: migrate, user, agent
        app/         gin handlers, middleware, request/response JSON
        bus/         business logic, validation, transactions, live fan-out
        storage/     repository over SQLite
            migrations/   goose SQL files, embedded
            queries/      sqlc query files
            sqlcgen/      sqlc output, committed
web/                 Svelte app; web/embed.go exposes the built files
plugin/              Hermes platform plugin (Python)
docs/
```

The module path is `github.com/quells-bot/hermes-chat` *(Kai)*.

*(Claude's calls)* `web/embed.go` is a tiny Go package at `web/` holding the
`//go:embed` directive, because embed cannot reach outside its own package
directory; `cmd/hermes-chat` imports it. Small leaf packages that import no
layer (for example `internal/reqid`, 6.3) may sit beside the three layers and be
imported by any of them.

## 4. The three layers

### 4.1 What each one does

| Layer | Owns | Must not |
|---|---|---|
| `app` | gin handlers, middleware, JSON request and response types, mapping bus errors to HTTP statuses, SSE framing | contain business rules; import `storage` |
| `bus` | every rule in the product spec (addressing, queueing, pause, turn cap, notes, auth decisions), validation, ID generation, transactions, the in-memory live fan-out | know about HTTP, gin, cookies, or JSON |
| `storage` | SQL, the two connection pools, migrations, mapping sqlc rows to storage types | make decisions; import `bus` or `app` |

The admin CLI is a second front end onto `bus`, alongside `app` (section 9).

### 4.2 Types *(Kai)*

- Each layer defines its own types.
- A layer may use the types of the layer directly below it, and maps them to
  its own types for what it returns. `app` cannot use anything from `storage`.
- Bus types should say more than strings and numbers: `ulid.ULID` IDs,
  `time.Time`, named types for message state, room flavor, and so on. Storage
  types stay close to the columns (text IDs, integer milliseconds).
- sqlc's generated structs stay inside `storage`. *(Claude's call)* Storage
  exposes its own hand-written types rather than `sqlcgen` types, so
  regenerating never changes what `bus` sees.

### 4.3 Interfaces and errors

As in catalog: `storage.Repository` and `bus.Service` are interfaces with one
unexported implementation each and a `New…` constructor, and each layer has its
own sentinel errors.

- `storage`: `ErrNotFound`, `ErrConflict`.
- `bus`: `ErrNotFound`, `ErrConflict`, `ErrValidation`, plus whatever the
  product rules need (unauthenticated, forbidden, room paused, …). The bus maps
  storage errors to its own; it never returns a storage sentinel.
- `app`: one `writeError` function maps bus sentinels to statuses and a JSON
  error body. Anything unrecognized is a 500 with a generic message, and the
  real error goes to the log.

*(Claude's call)* The product has far more operations than catalog. The builder
may split `Service` and `Repository` into several smaller interfaces by area
(rooms, messages, notes, auth, …) if a single interface becomes unwieldy, as
long as each layer still depends on interfaces from the layer below.

### 4.4 Who the caller is

*(Claude's call)* Middleware establishes who is calling (4.1, 6.2). Handlers
pass that actor to bus methods as an explicit argument. The bus does not read
the actor out of a context. Whether the actor may do the thing (room
membership, note ownership) is decided in the bus, not in middleware.

## 5. Process shape

### 5.1 Start-up *(Kai, except where marked)*

`serve` does, in order:

1. Load config with `kelseyhightower/envconfig`.
2. Build the JSON `slog` logger and set it as default.
3. `wireUp`: open the database pools, bring the schema up to date, build
   repository → service → controller, collect clean-up functions. *(Claude's
   call)* `wireUp` takes a mode: `serve` and `migrate` apply pending
   migrations; every other subcommand only checks the schema version and stops
   if it is behind (7.3).
4. Build the gin engine and call `routes`.
5. Build the runners and call `grace.Run` under `grace.StopOnInterrupt`.
6. After `grace.Run` returns, run the clean-up functions and exit non-zero if
   it returned an error.

### 5.2 Runners

Everything long-lived is a `grace.Runner`. `httpserver` below is
`github.com/quells/grace/pkg/httpserver`. v1 has three:

| Runner | What | Bound to |
|---|---|---|
| API server | `httpserver.New` with the gin engine | public address |
| Debug server | `httpserver.New` with `httpserver.DebugHandler()` (pprof), write timeout raised to 5 minutes | loopback only |
| Live fan-out | the bus's in-memory hub (section 8) | no socket |

Future background jobs (attachment clean-up, session expiry, and the like) are
added the same way. Nothing starts a goroutine that outlives a request except
through a runner.

### 5.3 Shutdown *(Claude's call)*

SSE streams never end on their own, so without help `http.Server.Shutdown`
would always wait out the whole grace period. The hub is therefore a runner:
when its context is cancelled it closes every subscription, the stream handlers
return, and the API server drains promptly inside grace's default 5 s.

### 5.4 Config

All configuration is environment variables read by envconfig *(Kai)*. No
config file, and no flags other than subcommand arguments.

*(Claude's calls)* Prefix `HERMES_CHAT_`. Suggested settings, names left to the
builder: public listen address, debug listen address (default
`127.0.0.1:6060`), database path, attachments directory, TLS mode
(`autocert` or `off`), TLS domain, certificate cache directory, log level.
Because the admin subcommands need only the database path, settings that only
`serve` needs (the TLS domain, for one) are validated by `serve`, not marked
required for every command.

## 6. HTTP

### 6.1 Router and middleware *(Kai)*

- **gin** for routing, in release mode unless configured otherwise.
- **Logging:** a custom middleware that writes one JSON `slog` line per
  request, replacing gin's default logger.
- **Recovery:** gin's default recovery middleware.
- **Auth:** one gin middleware per flavor of caller (6.2).

*(Claude's calls on the log line)* Fields: request ID, method, route pattern
(`c.FullPath()`, not the raw URL), status, duration, and the actor once known.
Never log the `Authorization` header, cookies, request bodies, or query
strings. An SSE stream logs once when it opens and once when it closes.

`routes.go` is the single place that shows every route, its group, and its
middleware. Three groups: public (login, `/healthz`, the web UI's static
files), human API, agent API.

### 6.2 Auth middleware

| Flavor | Credential | Checks |
|---|---|---|
| Human | session cookie | session exists and is live; on state-changing methods, the `Origin` header matches |
| Agent | bearer token | the token's SHA-256 matches a non-revoked agent |

Each middleware lives in `app`, asks the bus to resolve the credential, and on
success stores the actor in the gin context and aborts with 401 otherwise.
Login throttling (per username and per IP) is a bus rule, using
`golang.org/x/time/rate` held in memory *(Claude's call)*.

### 6.3 Request ID *(Kai)*

Every request gets a ULID request ID. It must be reachable from **both** the
gin context and the Go `context.Context`, so code in `bus` and `storage`, which
never sees gin, can log it.

*(Claude's calls)* The first middleware generates it, sets it on the gin
context, replaces `c.Request` with one whose context carries it, and returns it
in an `X-Request-ID` response header. The context key and its getter live in a
leaf package (`internal/reqid`) so all three layers can use it. The `slog`
handler is wrapped so that any `slog.…Context(ctx, …)` call adds the request ID
automatically; bus and storage code logs with the context variants and never
handles the ID itself.

### 6.4 TLS *(Claude's call, accepted by Kai)*

`golang.org/x/crypto/acme/autocert`, as the product spec already says. It
renews certificates itself, so there is no certificate runner. It is wired into
grace like this: build the `autocert.Manager`, open a TLS listener with the
manager's `TLSConfig()`, and pass it to `httpserver.New` with
`httpserver.WithListener`. That config answers the TLS-ALPN challenge on the
same port, so only 443 needs to be open. TLS mode `off` serves plain HTTP on
the same runner, for local development or behind a proxy. A redirect listener
on port 80 is not in v1; it would be one more runner.

### 6.5 SSE and timeouts *(Claude's call)*

grace's 30 s write timeout stays as the server default. Stream handlers lift it
for their own response with `http.ResponseController.SetWriteDeadline`. The
rest of the SSE guidance in the product spec (heartbeats, `X-Accel-Buffering`)
stands.

### 6.6 Small extras *(Claude's calls)*

An unauthenticated `GET /healthz` that returns 200 once the database answers.
`HEAD /healthz` gets the same status with no body.

### 6.7 Deploy

The target is one Debian 13 host, run under systemd. The runbook is
`docs/deploy.md`.

- **Layout** *(Claude's call, accepted by Kai)*. Binary
  `/usr/local/bin/hermes-chat`, the previous one beside it as
  `hermes-chat.previous`, environment file `/etc/hermes-chat/env`, state in
  `/var/lib/hermes-chat` (`chat.db`, `attachments/`, `certs/`, `backups/`),
  system user and group `hermes-chat`, unit `hermes-chat.service`, and the
  scripts `/usr/local/sbin/hermes-chat-upgrade` and
  `/usr/local/sbin/hermes-chat-admin`.
- **No deploy command** *(Kai)*. There is no `just deploy`. The operator builds,
  copies the binary with `scp`, and runs `hermes-chat-upgrade` on the host.
- **The repository names no instance** *(Kai)*. Committed files use
  `chat.example.com` and `203.0.113.10`.
- **Upgrade.** `serve` applies migrations at start, so an upgrade is: check the
  candidate, stop the unit, copy the database aside, swap the binary, start,
  and wait for `/healthz`. A failed health check restores the old binary and
  the database as it was. The newest five database copies are kept.
- **Restart policy** *(Claude's call)*. The unit restarts the server when it
  fails (`Restart=on-failure`, `RestartSec=2`) and gives up after five failed
  starts in a minute (`StartLimitIntervalSec=60`, `StartLimitBurst=5`), which
  leaves the unit failed instead of restarting for ever. `hermes-chat-upgrade`
  clears that limit (`systemctl reset-failed`) before each start, and stops
  waiting for `/healthz` as soon as the unit has given up.
- **TLS.** autocert answers the TLS-ALPN challenge on 443. There is no port-80
  listener and no redirect (section 6.4).
- **Host firewall.** ufw allows 22 and 443 and nothing else; turning it on is a
  step in the runbook.
- **Backups.** Scheduled off-host backups are out of scope for stage 7. The
  runbook says what to copy by hand.

## 7. Storage

### 7.1 SQLite settings *(Kai, except the cache size)*

Driver: `modernc.org/sqlite` (pure Go, from the product spec).

```
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous  = NORMAL;
PRAGMA cache_size   = -64000;
PRAGMA foreign_keys = true;
PRAGMA temp_store   = memory;
```

- `BEGIN IMMEDIATE` for every transaction.
- Every table is `STRICT`.
- Two pools on the one file: a write pool with `SetMaxOpenConns(1)` and a read
  pool with `SetMaxOpenConns(max(4, runtime.NumCPU()))`.

`cache_size` *(Claude's call, agreed by Kai)*: Kai's starting value was
`1000000000`. A positive value is a page count, so that is effectively
unlimited, and each connection could grow its cache to the size of the whole
database. With history kept forever on a small VPS it is capped at about 64 MB
per connection instead.

*(Claude's calls)*

- **Pragmas go in the connection string** (`_pragma=…` parameters, and
  `_txlock=immediate` on the write pool), so every connection a pool opens gets
  them. A one-off `PRAGMA` statement would reach only one connection.
- **The read pool is opened with `query_only`**, so a write sent to the wrong
  pool fails loudly instead of contending with the writer.

### 7.2 Repository and transactions

The repository holds both pools. Plain reads use the read pool. Single writes
use the write pool.

**The bus owns transactions** *(Kai)*. The repository exposes

```go
InTx(ctx context.Context, fn func(Repository) error) error
```

implemented with sqlc's `WithTx`: begin on the write pool, hand `fn` a
repository bound to that transaction, commit if `fn` returns nil, roll back
otherwise. Everything inside `fn`, reads included, runs on that one write
connection. The bus composes several repository calls inside `InTx` when they
must land together; posting a message (insert the message, decide who is woken,
write queue rows, write the event) is the model case. Repository methods stay
small and do one thing each.

*(Claude's calls)* Rules that follow from one write connection:

- A transaction callback does database work only. No network calls, no waiting
  on channels, no publishing to the hub. While it runs, every other write in
  the process is waiting.
- Publish to the live fan-out **after** `InTx` returns nil, never inside it, so
  nobody is told about a change that then rolls back.
- Calling `InTx` on a repository already inside a transaction is an error, not
  a nested transaction.

### 7.3 Migrations and queries *(Kai)*

- **goose** for migrations: plain SQL files in `storage/migrations`, embedded
  in the binary and applied through goose's Go library.
- **sqlc** for queries: `.sql` files in `storage/queries`, generated into
  `storage/sqlcgen`.

*(Claude's calls)*

- sqlc reads the goose migration files as its schema, so there is one
  definition of the tables.
- Migrations are applied at the start of `serve` and by a `migrate`
  subcommand. Other subcommands do not migrate; if the schema is behind they
  stop with a message saying to run `migrate`.
- Generated code is committed. A just recipe regenerates and fails if the
  working tree changed.
- The goose command-line tool is not needed; new migration files are created
  by hand following goose's naming.

### 7.4 IDs and time

- **ULIDs for everything, never UUIDs** *(Kai)*, using
  `github.com/oklog/ulid/v2`. Generated in the bus.
- **ULID text is both primary key and foreign key** *(Claude's call; Kai was
  indifferent between this and integer row IDs)*. One ID per entity, no
  translating between an internal and a public ID, and rows are readable in
  the `sqlite3` shell. The size cost is irrelevant at this scale.
- **The one exception: the event log is keyed by an autoincrement integer**
  *(agreed by Kai)*. Its ID is the SSE cursor, so "everything after the last
  one I saw" must mean commit order exactly. SQLite's single writer guarantees
  that for an autoincrement key; a ULID, generated before commit and dependent
  on the clock, does not. Nothing else references an event by ID.
- **Timestamps** *(Claude's call)*: `STRICT` has no date type, so they are
  stored as integer Unix milliseconds in UTC and become `time.Time` in the bus.

## 8. Live fan-out

The product spec calls for an event log in the database as each client's
cursor, with token-by-token streaming edits fanned out from memory. In this
layout *(Claude's call)*:

- The hub is a component of `bus`. It holds the set of live subscriptions and
  nothing durable.
- `app`'s stream handlers subscribe through a bus method and turn what they
  receive into SSE frames. `app` knows SSE; the bus knows who is entitled to
  what.
- The bus publishes to the hub after the transaction that recorded the change
  commits (7.2).
- The hub is a runner so it can close subscriptions on shutdown (5.3).
- **Token revocation.** The admin CLI is a separate process and cannot reach
  the server's memory, so "revoking a token closes that agent's stream" works
  by the stream handler re-checking the token on each heartbeat. Revocation
  therefore takes effect within one heartbeat interval, not instantly.

How the hub is built inside (channels, buffer sizes, what happens to a slow
subscriber) is left to the builder.

## 9. Admin CLI

Subcommands of the same binary, as in the product spec: `serve`, `migrate`,
`user …`, `agent …`.

*(Claude's calls)* Dispatch with the standard library (`os.Args` and `flag`),
no CLI framework. Admin subcommands load the same config, call the same
`wireUp`, and call `bus` methods exactly as HTTP handlers do; they start no
runners and build no gin engine. They may run while the server is running;
WAL and the busy timeout cover the contention.

## 10. Testing

**Same approach as catalog** *(Kai)*: each layer is tested against the real
layers below it. Mocks appear only to force an error that cannot otherwise be
produced. This is cheap because the bottom is SQLite.

- `storage` tests run against a real database with migrations applied.
- `bus` tests build a real repository and a real service.
- `app` tests build repository → service → controller → gin engine and drive
  it with `httptest`.
- End-to-end tests in `cmd/hermes-chat` use the fake agent client from the
  product spec. *(Kai, 2026-10-12)* The client, `reqid`, and `sse` live in the
  repository-root `internal/` because they are reusable, and `cmd/fakeagent`
  wraps the client as a command. A comment, not the layering test, says the
  server must not import the client.

**testify** is allowed as a test-only dependency *(Kai)*.

*(Claude's calls)*

- `assert` and `require` from testify. Error-forcing mocks are hand-written:
  a struct that embeds the real interface and overrides one method, as catalog
  does. A mock that must fail inside a transaction also overrides `InTx` to
  hand the callback a wrapped repository.
- Black-box `_test` packages.
- **A temp-file database per test**, not `:memory:`. WAL does not work in
  memory, and two pools on `:memory:` would each see a different empty
  database. One shared helper opens a migrated database in `t.TempDir()`.
- **A layering test** reads the import graph and fails if `app` imports
  `storage`, or if anything imports upward.
- **The bus takes an injected clock**, so login throttling, the turn cap, and
  timeouts are tested without sleeping.

## 11. Tooling

Kai normally uses devbox, but it is not installed on the development machine.
**For now, use and document what is already installed; adjust later** *(Kai)*.
Installed on 2026-10-11:

| Tool | Version |
|---|---|
| Go | 1.26.5 |
| sqlc | 1.31.1 |
| Node / npm | 24.18.1 / 11.16.0 |
| Python / uv | 3.14.4 / 0.11.31 |
| just | 1.45.0 |
| shellcheck | 0.11.0 |
| sqlite3 shell | 3.46.1 |

Not installed and not required: goose CLI, devbox, golangci-lint.

**No Makefile** *(Kai)*. Nothing here produces intermediate artifacts worth
caching, and listing every input file to bust a cache is a chore. Tasks are run
with `just` and/or shell scripts; everything is expected to build quickly from
scratch each time.

**Build and deploy happen from an Apple silicon Mac; the server runs on a Linux
VPS** *(Kai)*. The binary is cross-compiled with `CGO_ENABLED=0`, which is why
the SQLite driver is the pure-Go `modernc.org/sqlite`. No dependency that needs
cgo may be added. *(Claude's call)* The build recipe takes the target OS and
architecture as parameters, since the VPS's architecture is not yet chosen.

**Scripts must run on the bash that ships with macOS (3.2)** *(Kai)*, because
that Mac is where they will run. That rules out
associative arrays, `mapfile`/`readarray`, `${var,,}` case conversion, `&>>`,
and `wait -n`, among other things. Also avoid GNU-only flags to `sed`, `date`,
`readlink`, and friends; macOS ships the BSD versions.

*(Claude's calls)*

- A `justfile` at the repository root is the entry point, with recipes for
  generate, check-generated, vet, test, web build, and build. `just` is already
  installed on the development machine and is one `brew install` on a Mac.
- A recipe longer than a few lines becomes a script in `scripts/` that the
  recipe calls, so the logic can be read, run, and linted on its own.
- Scripts start with `#!/usr/bin/env bash` and `set -euo pipefail`, and are
  checked with `shellcheck`.
- Linting for Go is `go vet` and nothing heavier.
- The README records the versions above.

## 12. Left to the builder

- Table design, query files, and the exact method sets of `Service` and
  `Repository`.
- Route paths and JSON shapes for the human and agent APIs.
- Config variable names and defaults (5.4).
- The hub's internals (section 8).
- How `go build` behaves before the web UI has been built (the embed directive
  needs the output directory to exist).

### To verify before relying on it

A reviewer checked the first three on the development machine on 2026-10-11;
the fourth was confirmed in stage 7.

- `modernc.org/sqlite` accepts `_pragma` and `_txlock` in the connection
  string. Confirmed by reading the driver source (versions 1.38 to 1.55), not
  by running it.
- sqlc 1.31.1's SQLite engine parses `STRICT` tables and goose-annotated
  migration files. Confirmed by generating code from a throwaway project.
- gin's response writer works with `http.ResponseController`. Confirmed: gin
  1.11 and 1.12 define `Unwrap()` on it.
- HTTP/2 is negotiated on the autocert listener when it is passed to
  `http.Server.Serve` through grace. Without it, browsers cap SSE streams at
  six per origin. Confirmed in stage 7 by `TestServe_AutocertNegotiatesHTTP2`.

## 13. Edits to the product spec

Made alongside this document so the two agree:

- Section 9: plain-HTTP mode is a config setting, not a flag; a pointer to this
  document.
- Section 7: revoking an agent token closes its stream within one heartbeat,
  not immediately.

## 14. Build stages

The server is built in eight stages *(seven agreed by Kai at the outset; the eighth added 2026-10-12)*. Each ends in something
that runs and is tested. Each stage gets its own implementation plan, written
only once the stage before it is merged, so every plan builds on real code
instead of guessed interfaces. Plans live in `docs/superpowers/plans/`.

1. **Skeleton.** Module and layout, envconfig, JSON `slog`, grace running the
   API and pprof servers, gin with request ID, logging and recovery, the two
   SQLite pools with goose, sqlc and `InTx`, the layering test, the `justfile`,
   and the `migrate` subcommand. Ends with a binary that serves `/healthz`,
   with every convention in this document exercised once. *(Claude's call)* To
   give sqlc, `InTx`, and the admin CLI something real to act on, this stage
   also creates the `humans` table (ID, username, created-at) and the
   `user add` and `user list` subcommands, without passwords.
2. **Identity and auth.** Passwords for humans, agents and their tokens, the
   rest of the admin CLI, sessions, login with throttling, both auth
   middlewares, and the bus-error-to-HTTP-status mapping.
3. **Rooms, messages, and the human stream.** Rooms and membership, posting and
   reply-to, the event log, the in-memory hub (as a runner), and SSE with
   cursors for browsers.
4. **Agent API and delivery.** The agent stream, addressing rules, the queue
   with one message per room and agent at a time, message states, turns and
   activity, streaming edits, presence, cancel, stop, and steer. The fake agent
   client is built here.
5. **The off switch.** Pause a room, stop everything, and the turn cap.
6. **Prompts, notes, and attachments.** Approvals and clarifying questions, the
   per-human note with its quarantine, and file storage.
7. **TLS and deploy.** Autocert, the HTTP/2 check, and the cross-compile and
   deploy scripts.
8. **Shapes for the web UI.** A client id that makes posting and steering safe
   to retry, `working` on a room's agents, and `streaming` on a message.

The web UI and the Hermes plugin follow as their own projects.
