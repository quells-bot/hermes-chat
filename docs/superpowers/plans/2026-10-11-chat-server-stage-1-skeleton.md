# Chat Server Stage 1 (Skeleton) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `hermes-chat` binary that serves `/healthz` and pprof under grace, migrates a SQLite database, and can add and list humans from the command line, with every convention in the architecture spec exercised once.

**Architecture:** Three layers under `cmd/hermes-chat/internal`: `app` (gin) → `bus` → `storage` (sqlc over two SQLite pools). `main` loads config, calls `wireUp`, and hands runners to `grace.Run`. Each layer has its own types and errors and is tested against the real layers below it.

**Tech Stack:** Go 1.26, gin, `kelseyhightower/envconfig`, `quells/grace`, `pressly/goose/v3` (library only), sqlc 1.31.1, `modernc.org/sqlite`, `oklog/ulid/v2`, testify, `just`.

**Spec:** `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (stage 1 of its section 14). Product context: `docs/superpowers/specs/2026-10-11-hermes-chat-design.md`. Read the architecture spec before starting any task.

## Global Constraints

- Module path: `github.com/quells-bot/hermes-chat`. `go 1.26` in `go.mod`.
- `CGO_ENABLED=0` must build. No dependency that needs cgo.
- `app` never imports `storage`. Nothing imports upward. `sqlcgen` types never leave `storage`.
- IDs are ULIDs (`github.com/oklog/ulid/v2`), never UUIDs. Stored as 26-character text.
- Every table is `STRICT`. Timestamps are stored as integer Unix milliseconds, UTC.
- SQLite pragmas, on every connection, set through the connection string: `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`, `cache_size=-64000`, `foreign_keys=true`, `temp_store=memory`. Write pool: `SetMaxOpenConns(1)`, `_txlock=immediate`. Read pool: `SetMaxOpenConns(max(4, runtime.NumCPU()))`, `query_only=true`.
- Tests: stdlib `testing` plus testify `assert`/`require`; black-box `_test` packages (except `package main`); a temp-file database per test, never `:memory:`; mocks only to force an error, written as a struct embedding the real interface.
- Logging: `log/slog` JSON. Code in `bus` and `storage` logs with the `…Context` variants only.
- No Makefile. Tasks run through `just`. Any shell script must run on bash 3.2 (macOS) and pass `shellcheck`.
- Not in this stage: TLS, auth, sessions, passwords, `writeError` status mapping, the hub, `web/`, `plugin/`.

## Review Focus

1. **A panic inside an `InTx` callback.** The single write connection must be released, or every later write in the process hangs forever. Pinned in Task 3.
2. **A database path containing spaces, `?`, `#`, or `%`.** The file at exactly that path must be opened, not a truncated or differently named one. Pinned in Task 2.
3. **A client-supplied `X-Request-ID` header.** It must be ignored: untrusted text must not reach the logs or be echoed back. Pinned in Task 5.
4. **An admin command run against a database that has not been migrated.** It must stop with a message naming `hermes-chat migrate`, exit non-zero, and create no tables. Pinned in Tasks 2 and 7.
5. **Missing or invalid configuration.** A missing database path or an unknown log level must produce an error naming the variable and a non-zero exit, not a panic or a silent default. Pinned in Task 6.

## File Structure

```
go.mod, go.sum
.gitignore                         add bin/ and SQLite files
justfile
sqlc.yaml
README.md
cmd/hermes-chat/
    main.go                        main(), run(): subcommand dispatch
    config.go                      Config, loadConfig()
    logging.go                     newLogger()
    wires.go                       wires, wireUp()
    routes.go                      routes()
    serve.go                       serve(): engine, runners, grace.Run
    admin.go                       migrate, user add, user list
    layering.go                    checkLayering() (pure), used by layering_test.go
    *_test.go
    internal/
        reqid/reqid.go             request ID context key and slog handler
        storage/
            storage.go             Repository, errors, NewRepo, InTx
            db.go                  DB, Open, Close, connection strings
            migrate.go             Migrate, CheckSchema, embedded migrations
            human.go               Human and its three methods
            export_test.go         test-only accessors
            migrations/00001_humans.sql
            queries/humans.sql
            sqlcgen/               generated, committed
            storagetest/storagetest.go
        bus/
            bus.go                 Service, errors, NewService, Clock, Health
            human.go               Human, CreateHuman, ListHumans
        app/
            app.go                 Controller, NewController, Healthz
            middleware.go          RequestID, RequestLogger
```

---

### Task 1: Module, task runner, and the `reqid` leaf package

**Files:**
- Create: `go.mod`, `justfile`, `cmd/hermes-chat/internal/reqid/reqid.go`
- Modify: `.gitignore`
- Test: `cmd/hermes-chat/internal/reqid/reqid_test.go`

**Interfaces:**
- Produces:
  ```go
  package reqid
  const GinKey = "request_id"   // key for gin's c.Set / c.Get
  const LogKey = "request_id"   // slog attribute name
  func NewContext(ctx context.Context, id ulid.ULID) context.Context
  func FromContext(ctx context.Context) (ulid.ULID, bool)
  func NewHandler(inner slog.Handler) slog.Handler
  ```
- Produces `just` recipes: `generate`, `check-generated`, `vet`, `test`, `build goos goarch`, `check`.

- [ ] **Step 1: Create the module and task runner**

`go mod init github.com/quells-bot/hermes-chat`, set `go 1.26`. Append to `.gitignore`: `bin/`, `*.db`, `*.db-wal`, `*.db-shm`.

`justfile`:

```just
generate:
    sqlc generate

check-generated:
    sqlc diff

vet:
    go vet ./...

test:
    go test ./...

build goos="linux" goarch="amd64":
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -o bin/hermes-chat-{{goos}}-{{goarch}} ./cmd/hermes-chat

check: check-generated vet test
```

`sqlc.yaml` does not exist until Task 3; `generate`, `check-generated`, and `check` will fail until then.

- [ ] **Step 2: Write the failing tests** in `reqid_test.go` (package `reqid_test`)

```go
func TestContextRoundTrip(t *testing.T) {
    id := ulid.Make()
    got, ok := reqid.FromContext(reqid.NewContext(context.Background(), id))
    require.True(t, ok)
    assert.Equal(t, id, got)
}

func TestFromContext_Absent(t *testing.T) {
    _, ok := reqid.FromContext(context.Background())
    assert.False(t, ok)
}

// logLine runs fn against a JSON logger wrapped by reqid.NewHandler and
// returns the single line it wrote, decoded.
func logLine(t *testing.T, fn func(*slog.Logger)) map[string]any

func TestHandler_AddsRequestID(t *testing.T) {
    id := ulid.Make()
    ctx := reqid.NewContext(context.Background(), id)
    line := logLine(t, func(l *slog.Logger) { l.InfoContext(ctx, "hi") })
    assert.Equal(t, id.String(), line["request_id"])
}

func TestHandler_NoRequestID_NoAttr(t *testing.T) {
    line := logLine(t, func(l *slog.Logger) { l.InfoContext(context.Background(), "hi") })
    assert.NotContains(t, line, "request_id")
}

// A logger derived with With or WithGroup must still be wrapped.
func TestHandler_SurvivesWithAndWithGroup(t *testing.T) {
    id := ulid.Make()
    ctx := reqid.NewContext(context.Background(), id)
    line := logLine(t, func(l *slog.Logger) { l.With("a", 1).InfoContext(ctx, "hi") })
    assert.Equal(t, id.String(), line["request_id"])
    assert.EqualValues(t, 1, line["a"])
    line = logLine(t, func(l *slog.Logger) { l.WithGroup("g").InfoContext(ctx, "hi", "b", 2) })
    assert.Equal(t, id.String(), line["request_id"]) // top level, not inside "g"
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/reqid/`
Expected: FAIL, package `reqid` has no such functions.

- [ ] **Step 4: Implement `reqid.go`**

`NewHandler` returns a handler whose `WithAttrs` and `WithGroup` return a new wrapped handler, never the bare inner one. The request ID must land at the top level even after `WithGroup`, so the wrapper cannot simply add the attribute to the record and pass it to an inner handler that already has a group open: it keeps the original ungrouped handler plus the list of `WithAttrs`/`WithGroup` calls made so far, and on `Handle` applies the request ID first and replays that list after it. This package imports no other package in this module.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./cmd/hermes-chat/internal/reqid/ && go vet ./...`
Expected: PASS, no vet output.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum .gitignore justfile cmd/hermes-chat/internal/reqid
git commit -m "Add Go module, justfile, and request ID package"
```

---

### Task 2: Storage: connection pools, pragmas, migrations

**Files:**
- Create: `cmd/hermes-chat/internal/storage/db.go`, `migrate.go`, `export_test.go`, `migrations/00001_humans.sql`, `storagetest/storagetest.go`
- Test: `cmd/hermes-chat/internal/storage/db_test.go`, `migrate_test.go`

**Interfaces:**
- Produces:
  ```go
  package storage
  var ErrSchemaBehind = errors.New("database schema is out of date")
  type DB struct{ /* read, write *sql.DB */ }
  func Open(ctx context.Context, path string) (*DB, error)
  func (d *DB) Close() error
  func (d *DB) Migrate(ctx context.Context) error      // apply all pending migrations
  func (d *DB) CheckSchema(ctx context.Context) error  // ErrSchemaBehind if any are pending

  // export_test.go (package storage, test builds only)
  func (d *DB) Pools() (read, write *sql.DB)
  func ReadDSN(path string) string
  func WriteDSN(path string) string

  package storagetest
  func Open(t testing.TB) *storage.DB   // migrated database in t.TempDir(), closed by t.Cleanup
  ```

- [ ] **Step 1: Write the migration** `migrations/00001_humans.sql`

```sql
-- +goose Up
CREATE TABLE humans (
    id         TEXT    NOT NULL PRIMARY KEY,
    username   TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE humans;
```

- [ ] **Step 2: Write the failing tests** (package `storage_test`)

```go
func open(t *testing.T) *storage.DB // storage.Open on a fresh t.TempDir() path, no migration, closed on cleanup

func pragma(t *testing.T, q interface{ QueryRowContext(...) *sql.Row }, name string) string

func TestOpen_PragmasOnBothPools(t *testing.T) {
    read, write := open(t).Pools()
    for _, p := range []*sql.DB{read, write} {
        assert.Equal(t, "wal", pragma(t, p, "journal_mode"))
        assert.Equal(t, "5000", pragma(t, p, "busy_timeout"))
        assert.Equal(t, "1", pragma(t, p, "synchronous"))   // NORMAL
        assert.Equal(t, "-64000", pragma(t, p, "cache_size"))
        assert.Equal(t, "1", pragma(t, p, "foreign_keys"))
        assert.Equal(t, "2", pragma(t, p, "temp_store"))    // MEMORY
    }
    assert.Equal(t, "1", pragma(t, read, "query_only"))
    assert.Equal(t, "0", pragma(t, write, "query_only"))
}

// Pragmas come from the connection string, so every pooled connection has them.
func TestOpen_PragmasOnEveryReadConnection(t *testing.T) {
    read, _ := open(t).Pools()
    for i := 0; i < 3; i++ { // hold three connections open at once
        c, err := read.Conn(ctx); require.NoError(t, err); defer c.Close()
        assert.Equal(t, "1", pragma(t, c, "foreign_keys"))
        assert.Equal(t, "5000", pragma(t, c, "busy_timeout"))
    }
}

func TestOpen_PoolSizes(t *testing.T) {
    read, write := open(t).Pools()
    assert.Equal(t, 1, write.Stats().MaxOpenConnections)
    assert.Equal(t, max(4, runtime.NumCPU()), read.Stats().MaxOpenConnections)
}

func TestOpen_ReadPoolRejectsWrites(t *testing.T) {
    read, _ := open(t).Pools()
    _, err := read.ExecContext(ctx, "CREATE TABLE x (a INTEGER) STRICT")
    assert.Error(t, err)
}

func TestWriteDSN_TxLockImmediate(t *testing.T) {
    assert.Contains(t, storage.WriteDSN("/x/a.db"), "_txlock=immediate")
    assert.NotContains(t, storage.ReadDSN("/x/a.db"), "_txlock")
}

// Review Focus 2.
func TestOpen_PathWithSpecialCharacters(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "my db #1?100%.db")
    db, err := storage.Open(ctx, path); require.NoError(t, err); defer db.Close()
    require.NoError(t, db.Migrate(ctx))
    _, err = os.Stat(path); assert.NoError(t, err)
    entries, _ := os.ReadDir(dir)
    for _, e := range entries { // only the database and its -wal / -shm siblings
        assert.True(t, strings.HasPrefix(e.Name(), "my db #1?100%.db"), e.Name())
    }
}

func tables(t *testing.T, db *storage.DB) []string // names from sqlite_master where type='table'

func TestMigrate_CreatesStrictHumans(t *testing.T) {
    db := open(t); require.NoError(t, db.Migrate(ctx))
    assert.Contains(t, tables(t, db), "humans")
    _, write := db.Pools()
    _, err := write.ExecContext(ctx, `INSERT INTO humans (id, username, created_at) VALUES ('a','b','not a number')`)
    assert.Error(t, err) // STRICT rejects text in an INTEGER column
}

func TestMigrate_Idempotent(t *testing.T) {
    db := open(t)
    require.NoError(t, db.Migrate(ctx)); require.NoError(t, db.Migrate(ctx))
}

// Review Focus 4.
func TestCheckSchema(t *testing.T) {
    db := open(t)
    assert.ErrorIs(t, db.CheckSchema(ctx), storage.ErrSchemaBehind)
    assert.Empty(t, tables(t, db)) // checking must not create anything
    require.NoError(t, db.Migrate(ctx))
    assert.NoError(t, db.CheckSchema(ctx))
}

func TestStoragetestOpen_IsMigrated(t *testing.T) {
    assert.NoError(t, storagetest.Open(t).CheckSchema(ctx))
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/storage/...`
Expected: FAIL, undefined `storage.Open`.

- [ ] **Step 4: Implement `db.go`**

Driver `modernc.org/sqlite` (driver name `sqlite`). The connection string is a `file:` URI with the path escaped, one `_pragma=name(value)` parameter per pragma in Global Constraints, plus `_txlock=immediate` (write) or `_pragma=query_only(true)` (read). `Open` opens and pings the write pool first, so the file exists and is in WAL mode before the read pool connects. `Close` closes both pools and joins the errors.

- [ ] **Step 5: Implement `migrate.go`**

Embed `migrations/*.sql`. Use goose's provider API (`goose.NewProvider(goose.DialectSQLite3, db, fsys)`), not its package-level functions, so there is no global state. `Migrate` calls `Up` on the write pool. `CheckSchema` returns `ErrSchemaBehind` when anything is pending and must not create goose's version table; if the provider's `HasPending` creates it, compare the embedded migration versions against the version table yourself and treat a missing table as "all pending". If goose blocks on the single-connection write pool, run migrations on a short-lived `*sql.DB` opened with `WriteDSN` and closed afterwards.

- [ ] **Step 6: Implement `export_test.go` and `storagetest.Open`**

- [ ] **Step 7: Run to verify pass**

Run: `go test ./cmd/hermes-chat/internal/storage/... && go vet ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum cmd/hermes-chat/internal/storage
git commit -m "Add SQLite pools, pragmas, and goose migrations"
```

---

### Task 3: Storage: sqlc, the humans repository, and `InTx`

**Files:**
- Create: `sqlc.yaml`, `cmd/hermes-chat/internal/storage/queries/humans.sql`, `storage.go`, `human.go`, `sqlcgen/` (generated)
- Modify: `cmd/hermes-chat/internal/storage/storagetest/storagetest.go`, `export_test.go` (add `func (d *DB) Path() string`, the path given to `Open`)
- Test: `cmd/hermes-chat/internal/storage/storage_test.go`

**Interfaces:**
- Consumes: `storage.DB`, `(*DB).Pools` (tests), `storagetest.Open` from Task 2.
- Produces:
  ```go
  package storage
  var (
      ErrNotFound = errors.New("not found")
      ErrConflict = errors.New("conflict")
      ErrNestedTx = errors.New("InTx called inside a transaction")
  )
  type Human struct {
      ID          string // ULID text
      Username    string
      CreatedAtMs int64  // Unix milliseconds, UTC
  }
  type Repository interface {
      Ping(ctx context.Context) error
      InTx(ctx context.Context, fn func(Repository) error) error
      PutHuman(ctx context.Context, h Human) error                        // ErrConflict on duplicate id or username
      GetHumanByUsername(ctx context.Context, username string) (Human, error) // ErrNotFound
      ListHumans(ctx context.Context) ([]Human, error)                    // ordered by username
  }
  func NewRepo(db *DB) *repo

  package storagetest
  func NewRepo(t testing.TB) storage.Repository // over storagetest.Open(t)
  ```

- [ ] **Step 1: Write `sqlc.yaml` and the queries, then generate**

```yaml
version: "2"
sql:
  - engine: sqlite
    schema: cmd/hermes-chat/internal/storage/migrations
    queries: cmd/hermes-chat/internal/storage/queries
    gen:
      go:
        package: sqlcgen
        out: cmd/hermes-chat/internal/storage/sqlcgen
```

```sql
-- name: InsertHuman :exec
INSERT INTO humans (id, username, created_at) VALUES (?, ?, ?);

-- name: GetHumanByUsername :one
SELECT id, username, created_at FROM humans WHERE username = ?;

-- name: ListHumans :many
SELECT id, username, created_at FROM humans ORDER BY username;
```

Run: `just generate && just check-generated`
Expected: `sqlcgen/` contains `db.go`, `models.go`, `humans.sql.go`; `sqlc diff` prints nothing and exits 0.

- [ ] **Step 2: Write the failing tests** (package `storage_test`)

```go
var kai = storage.Human{ID: "01JA0000000000000000000001", Username: "kai", CreatedAtMs: 1791700000000}
var ada = storage.Human{ID: "01JA0000000000000000000002", Username: "ada", CreatedAtMs: 1791700000001}

func TestPing(t *testing.T) { assert.NoError(t, storagetest.NewRepo(t).Ping(ctx)) }

func TestPutAndGetHuman(t *testing.T) {
    r := storagetest.NewRepo(t)
    require.NoError(t, r.PutHuman(ctx, kai))
    got, err := r.GetHumanByUsername(ctx, "kai")
    require.NoError(t, err); assert.Equal(t, kai, got)
}

func TestGetHuman_NotFound(t *testing.T) {
    _, err := storagetest.NewRepo(t).GetHumanByUsername(ctx, "nobody")
    assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestPutHuman_Conflict(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutHuman(ctx, kai))
    sameName := storage.Human{ID: ada.ID, Username: "kai"}
    sameID := storage.Human{ID: kai.ID, Username: "other"}
    assert.ErrorIs(t, r.PutHuman(ctx, sameName), storage.ErrConflict)
    assert.ErrorIs(t, r.PutHuman(ctx, sameID), storage.ErrConflict)
}

func TestListHumans_OrderedByUsername(t *testing.T) {
    r := storagetest.NewRepo(t)
    require.NoError(t, r.PutHuman(ctx, kai)); require.NoError(t, r.PutHuman(ctx, ada))
    got, err := r.ListHumans(ctx); require.NoError(t, err)
    assert.Equal(t, []storage.Human{ada, kai}, got)
}

func TestListHumans_EmptyIsEmptyNotNil(t *testing.T) {
    got, err := storagetest.NewRepo(t).ListHumans(ctx)
    require.NoError(t, err); assert.NotNil(t, got); assert.Empty(t, got)
}

func TestInTx_CommitsTogether(t *testing.T) {
    r := storagetest.NewRepo(t)
    require.NoError(t, r.InTx(ctx, func(tx storage.Repository) error {
        if err := tx.PutHuman(ctx, kai); err != nil { return err }
        return tx.PutHuman(ctx, ada)
    }))
    got, _ := r.ListHumans(ctx); assert.Len(t, got, 2)
}

func TestInTx_RollsBackAndReturnsTheCallbackError(t *testing.T) {
    r := storagetest.NewRepo(t); boom := errors.New("boom")
    err := r.InTx(ctx, func(tx storage.Repository) error {
        require.NoError(t, tx.PutHuman(ctx, kai)); return boom
    })
    assert.ErrorIs(t, err, boom)
    got, _ := r.ListHumans(ctx); assert.Empty(t, got)
}

func TestInTx_ReadsSeeOwnWrites(t *testing.T) {
    r := storagetest.NewRepo(t)
    require.NoError(t, r.InTx(ctx, func(tx storage.Repository) error {
        require.NoError(t, tx.PutHuman(ctx, kai))
        _, err := tx.GetHumanByUsername(ctx, "kai"); assert.NoError(t, err)   // inside: visible
        _, err = r.GetHumanByUsername(ctx, "kai"); assert.ErrorIs(t, err, storage.ErrNotFound) // outside: not yet
        return nil
    }))
}

func TestInTx_NestedIsAnError(t *testing.T) {
    r := storagetest.NewRepo(t)
    err := r.InTx(ctx, func(tx storage.Repository) error {
        return tx.InTx(ctx, func(storage.Repository) error { return nil })
    })
    assert.ErrorIs(t, err, storage.ErrNestedTx)
}

// Review Focus 1: a panic must not leave the only write connection checked out.
func TestInTx_PanicReleasesTheWriteConnection(t *testing.T) {
    r := storagetest.NewRepo(t)
    assert.Panics(t, func() {
        _ = r.InTx(ctx, func(tx storage.Repository) error {
            require.NoError(t, tx.PutHuman(ctx, kai)); panic("boom")
        })
    })
    short, cancel := context.WithTimeout(ctx, 2*time.Second); defer cancel()
    require.NoError(t, r.PutHuman(short, ada))  // would block forever if the connection leaked
    got, _ := r.ListHumans(ctx); assert.Equal(t, []storage.Human{ada}, got) // kai rolled back
}

// BEGIN IMMEDIATE: the write lock is held from the start of the transaction,
// before the callback writes anything.
func TestInTx_TakesWriteLockAtBegin(t *testing.T) {
    db := storagetest.Open(t); r := storage.NewRepo(db)
    path := db.Path()
    other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)"); require.NoError(t, err); defer other.Close()
    require.NoError(t, r.InTx(ctx, func(storage.Repository) error {
        _, err := other.ExecContext(ctx, "BEGIN IMMEDIATE")
        assert.Error(t, err) // SQLITE_BUSY: our transaction already holds the lock
        return nil
    }))
}

func TestInTx_CancelledContext(t *testing.T) {
    r := storagetest.NewRepo(t); c, cancel := context.WithCancel(ctx); cancel()
    called := false
    err := r.InTx(c, func(storage.Repository) error { called = true; return nil })
    assert.Error(t, err); assert.False(t, called)
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/storage/...`
Expected: FAIL, undefined `storage.Repository`, `storagetest.NewRepo`.

- [ ] **Step 4: Implement `storage.go` and `human.go`**

`repo` holds the `*DB`, a `sqlcgen.Queries` for reads (read pool), one for writes (write pool), and whether it is inside a transaction. `InTx` begins on the write pool, builds a `repo` whose read and write queries are both `WithTx(tx)`, and rolls back in a `defer` unless it committed, so a panic rolls back and then continues to propagate. Map a modernc `*sqlite.Error` whose `Code()` is `SQLITE_CONSTRAINT_UNIQUE` (2067) or `SQLITE_CONSTRAINT_PRIMARYKEY` (1555) to `ErrConflict`, and `sql.ErrNoRows` to `ErrNotFound`. `Ping` pings both pools. `sqlcgen` types appear only inside this package's function bodies.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./cmd/hermes-chat/internal/storage/... && just check`
Expected: PASS; `sqlc diff` clean.

- [ ] **Step 6: Commit**

```bash
git add sqlc.yaml cmd/hermes-chat/internal/storage
git commit -m "Add sqlc queries, humans repository, and InTx"
```

---

### Task 4: Bus: service, clock, humans

**Files:**
- Create: `cmd/hermes-chat/internal/bus/bus.go`, `human.go`
- Test: `cmd/hermes-chat/internal/bus/bus_test.go`

**Interfaces:**
- Consumes: `storage.Repository`, `storage.Human`, `storage.ErrConflict`, `storagetest.NewRepo`.
- Produces:
  ```go
  package bus
  var (
      ErrNotFound   = errors.New("not found")
      ErrConflict   = errors.New("conflict")
      ErrValidation = errors.New("validation failed")
  )
  type Clock func() time.Time
  type Human struct {
      ID        ulid.ULID
      Username  string
      CreatedAt time.Time // UTC, millisecond precision
  }
  type Service interface {
      Health(ctx context.Context) error
      CreateHuman(ctx context.Context, username string) (Human, error)
      ListHumans(ctx context.Context) ([]Human, error)
  }
  func NewService(repo storage.Repository, clock Clock) *service
  ```

Username rule *(this plan's call)*: trim surrounding whitespace and lowercase, then require a match of `^[a-z0-9][a-z0-9_-]{0,31}$`.

- [ ] **Step 1: Write the failing tests** (package `bus_test`)

```go
var fixed = time.Date(2026, 10, 11, 12, 0, 0, 123_000_000, time.UTC)
func newSvc(t *testing.T) bus.Service { return bus.NewService(storagetest.NewRepo(t), func() time.Time { return fixed }) }

func TestHealth_OK(t *testing.T) { assert.NoError(t, newSvc(t).Health(ctx)) }

func TestCreateHuman(t *testing.T) {
    h, err := newSvc(t).CreateHuman(ctx, "kai"); require.NoError(t, err)
    assert.Equal(t, "kai", h.Username)
    assert.Equal(t, fixed, h.CreatedAt)
    assert.Equal(t, time.UTC, h.CreatedAt.Location())
    assert.Equal(t, uint64(fixed.UnixMilli()), h.ID.Time()) // the ULID's timestamp comes from the injected clock
}

func TestCreateHuman_NormalizesUsername(t *testing.T) {
    h, err := newSvc(t).CreateHuman(ctx, "  Kai_W-2 "); require.NoError(t, err)
    assert.Equal(t, "kai_w-2", h.Username)
}

func TestCreateHuman_Validation(t *testing.T) {
    for _, bad := range []string{"", "   ", "-kai", "_kai", "kai wells", "käi", "kai!", strings.Repeat("a", 33)} {
        _, err := newSvc(t).CreateHuman(ctx, bad)
        assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
    }
    _, err := newSvc(t).CreateHuman(ctx, strings.Repeat("a", 32)); assert.NoError(t, err)
}

func TestCreateHuman_DuplicateIsConflict(t *testing.T) {
    s := newSvc(t); _, err := s.CreateHuman(ctx, "kai"); require.NoError(t, err)
    _, err = s.CreateHuman(ctx, "KAI")
    assert.ErrorIs(t, err, bus.ErrConflict)
    assert.NotErrorIs(t, err, storage.ErrConflict) // the bus never returns a storage sentinel
}

func TestListHumans_MapsAndOrders(t *testing.T) {
    s := newSvc(t); _, _ = s.CreateHuman(ctx, "kai"); _, _ = s.CreateHuman(ctx, "ada")
    got, err := s.ListHumans(ctx); require.NoError(t, err)
    require.Len(t, got, 2)
    assert.Equal(t, "ada", got[0].Username); assert.Equal(t, "kai", got[1].Username)
    assert.Equal(t, fixed, got[0].CreatedAt)
    assert.NotEqual(t, got[0].ID, got[1].ID)
}

// Mock only to force an error the real repository cannot produce on demand.
type brokenRepo struct{ storage.Repository }
var errDisk = errors.New("disk on fire")
func (brokenRepo) Ping(context.Context) error                        { return errDisk }
func (brokenRepo) ListHumans(context.Context) ([]storage.Human, error) { return nil, errDisk }

func TestUnexpectedStorageErrorsPassThrough(t *testing.T) {
    s := bus.NewService(brokenRepo{storagetest.NewRepo(t)}, time.Now)
    assert.ErrorIs(t, s.Health(ctx), errDisk)
    _, err := s.ListHumans(ctx); assert.ErrorIs(t, err, errDisk)
}

// A row whose ID is not a ULID is a corrupted database, reported as an error, not a panic.
type badIDRepo struct{ storage.Repository }
func (badIDRepo) ListHumans(context.Context) ([]storage.Human, error) {
    return []storage.Human{{ID: "not-a-ulid", Username: "x"}}, nil
}
func TestListHumans_BadStoredID(t *testing.T) {
    _, err := bus.NewService(badIDRepo{storagetest.NewRepo(t)}, time.Now).ListHumans(ctx)
    assert.Error(t, err)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/bus/`
Expected: FAIL, undefined `bus.NewService`.

- [ ] **Step 3: Implement `bus.go` and `human.go`**

IDs: `ulid.New(ulid.Timestamp(clock()), ulid.DefaultEntropy())`. `CreatedAt` is `clock()` in UTC truncated to milliseconds, so it survives the round trip through storage unchanged. Validation errors wrap `ErrValidation` with `fmt.Errorf("…: %w", ErrValidation)` and a message a person can act on. `Health` returns `repo.Ping`'s error unchanged.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/hermes-chat/internal/bus/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/hermes-chat/internal/bus
git commit -m "Add bus service with clock, ULIDs, and humans"
```

---

### Task 5: App: health handler, request ID, request logging

**Files:**
- Create: `cmd/hermes-chat/internal/app/app.go`, `middleware.go`
- Test: `cmd/hermes-chat/internal/app/app_test.go`, `middleware_test.go`

**Interfaces:**
- Consumes: `bus.Service`, `bus.NewService`, `reqid.*`, `storagetest.NewRepo` (tests only).
- Produces:
  ```go
  package app
  const RequestIDHeader = "X-Request-ID"
  type Controller interface {
      Healthz(c *gin.Context)
  }
  func NewController(svc bus.Service) *controller
  func RequestID() gin.HandlerFunc
  func RequestLogger(log *slog.Logger) gin.HandlerFunc
  ```

Behavior pinned by this task:
- `Healthz`: 200 `{"status":"ok"}`; on error 503 `{"status":"unavailable"}`, with the error logged at ERROR through `slog.ErrorContext` and never sent to the client.
- `RequestID`: always generates a new ULID and ignores any inbound `X-Request-ID`. It sets `c.Set(reqid.GinKey, id)`, replaces `c.Request` with one whose context carries the ID, and sets the response header before calling `c.Next()`.
- `RequestLogger`: after `c.Next()`, one line with `msg` `"request"`, and attributes `method`, `route`, `status`, `duration_ms`. `route` is `c.FullPath()`, or `"unmatched"` when that is empty. Level ERROR for status ≥ 500, INFO otherwise. It logs with `log.…Context(c.Request.Context(), …)`, so the request ID arrives through the `reqid` handler and is not added by hand.

- [ ] **Step 1: Write the failing tests** (package `app_test`)

```go
// newEngine builds repository → service → controller → gin engine, logging JSON into buf.
func newEngine(t *testing.T, svc bus.Service) (*gin.Engine, *bytes.Buffer) {
    gin.SetMode(gin.TestMode)
    buf := &bytes.Buffer{}
    log := slog.New(reqid.NewHandler(slog.NewJSONHandler(buf, nil)))
    slog.SetDefault(log) // Healthz logs through the default logger
    e := gin.New()
    e.Use(app.RequestID(), app.RequestLogger(log), gin.Recovery())
    e.GET("/healthz", app.NewController(svc).Healthz)
    return e, buf
}
func realSvc(t *testing.T) bus.Service // bus.NewService(storagetest.NewRepo(t), time.Now)
func do(e *gin.Engine, method, target string, hdr ...string) *httptest.ResponseRecorder
func lines(buf *bytes.Buffer) []map[string]any // one decoded JSON object per log line

func TestHealthz_OK(t *testing.T) {
    e, _ := newEngine(t, realSvc(t))
    w := do(e, "GET", "/healthz")
    assert.Equal(t, 200, w.Code); assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

type sickSvc struct{ bus.Service }
func (sickSvc) Health(context.Context) error { return errors.New("disk on fire") }

func TestHealthz_Unavailable(t *testing.T) {
    e, buf := newEngine(t, sickSvc{realSvc(t)})
    w := do(e, "GET", "/healthz")
    assert.Equal(t, 503, w.Code); assert.JSONEq(t, `{"status":"unavailable"}`, w.Body.String())
    assert.NotContains(t, w.Body.String(), "disk on fire")
    var found bool
    for _, l := range lines(buf) {
        if l["level"] == "ERROR" && strings.Contains(fmt.Sprint(l), "disk on fire") {
            found = true
            assert.Equal(t, w.Header().Get(app.RequestIDHeader), l["request_id"])
        }
    }
    assert.True(t, found, "the real error is logged")
}

func TestRequestID_HeaderGinAndGoContextAgree(t *testing.T) {
    e, _ := newEngine(t, realSvc(t))
    var fromGin, fromCtx string
    e.GET("/probe", func(c *gin.Context) {
        fromGin = c.MustGet(reqid.GinKey).(ulid.ULID).String()
        id, ok := reqid.FromContext(c.Request.Context()); require.True(t, ok); fromCtx = id.String()
    })
    w := do(e, "GET", "/probe")
    hdr := w.Header().Get(app.RequestIDHeader)
    _, err := ulid.ParseStrict(hdr); require.NoError(t, err)
    assert.Equal(t, hdr, fromGin); assert.Equal(t, hdr, fromCtx)
}

func TestRequestID_UniquePerRequest(t *testing.T) {
    e, _ := newEngine(t, realSvc(t))
    a := do(e, "GET", "/healthz").Header().Get(app.RequestIDHeader)
    b := do(e, "GET", "/healthz").Header().Get(app.RequestIDHeader)
    assert.NotEqual(t, a, b)
}

// Review Focus 3.
func TestRequestID_IgnoresClientSuppliedHeader(t *testing.T) {
    e, buf := newEngine(t, realSvc(t))
    w := do(e, "GET", "/healthz", app.RequestIDHeader, "attacker-chosen")
    assert.NotEqual(t, "attacker-chosen", w.Header().Get(app.RequestIDHeader))
    assert.NotContains(t, buf.String(), "attacker-chosen")
}

func TestRequestLogger_OneLineWithRoutePattern(t *testing.T) {
    e, buf := newEngine(t, realSvc(t))
    e.GET("/things/:id", func(c *gin.Context) { c.Status(204) })
    w := do(e, "GET", "/things/42?token=hunter2", "Authorization", "Bearer s3cr3t", "Cookie", "sid=c00kie")
    ls := lines(buf); require.Len(t, ls, 1); l := ls[0]
    assert.Equal(t, "request", l["msg"]); assert.Equal(t, "INFO", l["level"])
    assert.Equal(t, "GET", l["method"]); assert.Equal(t, "/things/:id", l["route"])
    assert.EqualValues(t, 204, l["status"])
    assert.IsType(t, float64(0), l["duration_ms"])
    assert.Equal(t, w.Header().Get(app.RequestIDHeader), l["request_id"])
    for _, secret := range []string{"hunter2", "token", "s3cr3t", "c00kie", "/things/42"} {
        assert.NotContains(t, buf.String(), secret)
    }
}

func TestRequestLogger_Unmatched(t *testing.T) {
    e, buf := newEngine(t, realSvc(t))
    do(e, "GET", "/nope/secret-path")
    l := lines(buf)[0]
    assert.Equal(t, "unmatched", l["route"]); assert.EqualValues(t, 404, l["status"])
    assert.NotContains(t, buf.String(), "secret-path")
}

func TestRequestLogger_PanicIsRecoveredAndLoggedAsError(t *testing.T) {
    e, buf := newEngine(t, realSvc(t))
    e.GET("/boom", func(*gin.Context) { panic("boom") })
    w := do(e, "GET", "/boom")
    assert.Equal(t, 500, w.Code)
    var req map[string]any
    for _, l := range lines(buf) { if l["msg"] == "request" { req = l } }
    require.NotNil(t, req)
    assert.Equal(t, "ERROR", req["level"]); assert.EqualValues(t, 500, req["status"])
}
```

gin's recovery middleware prints the panic to `gin.DefaultErrorWriter` in its own format; that is expected and the last test only reads the `request` line. Set `gin.DefaultErrorWriter = io.Discard` in that test to keep its output quiet.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/app/`
Expected: FAIL, undefined `app.RequestID`.

- [ ] **Step 3: Implement `app.go` and `middleware.go`** to the behavior listed above.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/hermes-chat/internal/app/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/hermes-chat/internal/app
git commit -m "Add health handler, request ID, and request logging middleware"
```

---

### Task 6: Command: config, logger, wiring, routes, `serve`, `migrate`

**Files:**
- Create: `cmd/hermes-chat/main.go`, `config.go`, `logging.go`, `wires.go`, `routes.go`, `serve.go`, `admin.go`
- Test: `cmd/hermes-chat/config_test.go`, `main_test.go`, `serve_test.go` (all `package main`)

**Interfaces:**
- Consumes: everything produced by Tasks 1 to 5; `grace.Run`, `grace.StopOnInterrupt`, and `github.com/quells/grace/pkg/httpserver` (`New`, `DebugHandler`, `WithListener`, `WithLogger`, `WithLoggerLabel`, `WithWriteTimeout`).
- Produces (all in `package main`):
  ```go
  type Config struct {
      HTTPAddr  string `envconfig:"HTTP_ADDR"  default:":8080"`
      DebugAddr string `envconfig:"DEBUG_ADDR" default:"127.0.0.1:6060"`
      DBPath    string `envconfig:"DB_PATH"    required:"true"`
      LogLevel  string `envconfig:"LOG_LEVEL"  default:"info"`   // debug | info | warn | error
      GinMode   string `envconfig:"GIN_MODE"   default:"release"`
  }
  func loadConfig() (Config, error)            // envconfig.Process("HERMES_CHAT", …), then validates: DBPath non-empty, LogLevel known, GinMode one of debug | release | test
  func (c Config) slogLevel() slog.Level
  func newLogger(w io.Writer, level slog.Level) *slog.Logger  // JSON, wrapped by reqid.NewHandler

  type wireMode int
  const ( wireMigrate wireMode = iota; wireCheck )
  type wires struct {
      Svc     bus.Service
      App     app.Controller
      CleanUp []func() error
  }
  func wireUp(ctx context.Context, cfg Config, mode wireMode) (wires, error)
  func (w wires) cleanUp()   // runs CleanUp in reverse order, ignoring errors

  func routes(e *gin.Engine, ctrl app.Controller, log *slog.Logger)
  type listeners struct{ api, debug net.Listener } // nil: listen on the configured address
  func serve(ctx context.Context, cfg Config, log *slog.Logger, ln listeners) error
  func run(ctx context.Context, args []string, stdout, stderr io.Writer) int
  ```

Behavior pinned by this task:
- `run` exit codes: 0 success, 1 failure, 2 usage error (no subcommand or an unknown one; usage text goes to `stderr`). The logger writes to `stderr`; command output goes to `stdout`.
- `run` loads config first; a config error prints `hermes-chat: <error>` to `stderr` and returns 1.
- `wireUp`: `wireMigrate` calls `db.Migrate`; `wireCheck` calls `db.CheckSchema` and returns its error. On any failure it still returns the clean-up functions collected so far.
- `migrate`: `wireUp(…, wireMigrate)`, clean up, print `migrations applied` to `stdout`.
- `serve`: `slog.SetDefault(log)` (handlers and the bus log through the default logger); `wireUp(…, wireMigrate)`; `gin.SetMode(cfg.GinMode)`; `gin.New()`; `routes`; two runners (API with label `api`; debug with `httpserver.DebugHandler()`, write timeout 5 minutes, label `debug`); `grace.Run` under `grace.StopOnInterrupt`; clean up after it returns.
- `routes` applies `app.RequestID()`, `app.RequestLogger(log)`, `gin.Recovery()` in that order and registers `GET /healthz`. It is the only place routes or middleware are registered.

- [ ] **Step 1: Write the failing tests**

```go
// config_test.go
func TestLoadConfig_Defaults(t *testing.T) {
    t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
    c, err := loadConfig(); require.NoError(t, err)
    assert.Equal(t, Config{HTTPAddr: ":8080", DebugAddr: "127.0.0.1:6060", DBPath: "/tmp/x.db", LogLevel: "info", GinMode: "release"}, c)
}

// Review Focus 5.
func TestLoadConfig_MissingDBPath(t *testing.T) {
    os.Unsetenv("HERMES_CHAT_DB_PATH")
    _, err := loadConfig()
    require.Error(t, err); assert.Contains(t, err.Error(), "HERMES_CHAT_DB_PATH")
    t.Setenv("HERMES_CHAT_DB_PATH", "") // set but empty: envconfig's `required` accepts this, loadConfig must not
    _, err = loadConfig()
    require.Error(t, err); assert.Contains(t, err.Error(), "HERMES_CHAT_DB_PATH")
}

func TestLoadConfig_BadGinMode(t *testing.T) { // gin.SetMode panics on an unknown mode
    t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db"); t.Setenv("HERMES_CHAT_GIN_MODE", "fast")
    _, err := loadConfig()
    require.Error(t, err); assert.Contains(t, err.Error(), "HERMES_CHAT_GIN_MODE")
}

func TestLoadConfig_BadLogLevel(t *testing.T) {
    t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db"); t.Setenv("HERMES_CHAT_LOG_LEVEL", "loud")
    _, err := loadConfig()
    require.Error(t, err); assert.Contains(t, err.Error(), "HERMES_CHAT_LOG_LEVEL")
}

func TestNewLogger_JSONWithRequestID(t *testing.T) {
    var buf bytes.Buffer; id := ulid.Make()
    newLogger(&buf, slog.LevelInfo).InfoContext(reqid.NewContext(context.Background(), id), "hi")
    var l map[string]any; require.NoError(t, json.Unmarshal(buf.Bytes(), &l))
    assert.Equal(t, id.String(), l["request_id"])
    buf.Reset(); newLogger(&buf, slog.LevelWarn).Info("quiet"); assert.Empty(t, buf.String())
}

// main_test.go
func setDB(t *testing.T) string // sets HERMES_CHAT_DB_PATH to a path in t.TempDir() and returns it
func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string)

func TestRun_Usage(t *testing.T) {
    setDB(t)
    code, _, errOut := runCmd(t);            assert.Equal(t, 2, code); assert.Contains(t, errOut, "serve")
    code, _, _ = runCmd(t, "frobnicate");    assert.Equal(t, 2, code)
}

func TestRun_ConfigErrorExitsOne(t *testing.T) {
    os.Unsetenv("HERMES_CHAT_DB_PATH")
    code, _, errOut := runCmd(t, "migrate")
    assert.Equal(t, 1, code); assert.Contains(t, errOut, "HERMES_CHAT_DB_PATH")
}

func TestRun_Migrate(t *testing.T) {
    path := setDB(t)
    code, out, _ := runCmd(t, "migrate"); assert.Equal(t, 0, code); assert.Contains(t, out, "migrations applied")
    code, _, _ = runCmd(t, "migrate");    assert.Equal(t, 0, code) // idempotent
    db, err := storage.Open(context.Background(), path); require.NoError(t, err); defer db.Close()
    assert.NoError(t, db.CheckSchema(context.Background()))
}

func TestWireUp_CheckModeOnFreshDatabase(t *testing.T) {
    setDB(t); cfg, _ := loadConfig()
    w, err := wireUp(context.Background(), cfg, wireCheck); defer w.cleanUp()
    assert.ErrorIs(t, err, storage.ErrSchemaBehind)
}

// serve_test.go
// startServe runs serve on two 127.0.0.1:0 listeners and returns their base URLs,
// a cancel func, and a channel that receives serve's return value.
func startServe(t *testing.T) (api, debug string, cancel func(), done <-chan error)

func TestServe_EndToEnd(t *testing.T) {
    api, debug, cancel, done := startServe(t)

    resp, err := http.Get(api + "/healthz"); require.NoError(t, err)
    body, _ := io.ReadAll(resp.Body)
    assert.Equal(t, 200, resp.StatusCode); assert.JSONEq(t, `{"status":"ok"}`, string(body))
    _, err = ulid.ParseStrict(resp.Header.Get("X-Request-ID")); assert.NoError(t, err)

    resp, err = http.Get(debug + "/debug/pprof/"); require.NoError(t, err)
    assert.Equal(t, 200, resp.StatusCode)

    resp, err = http.Get(api + "/debug/pprof/"); require.NoError(t, err)
    assert.Equal(t, 404, resp.StatusCode) // pprof is never on the public server

    cancel()
    select {
    case err := <-done: assert.NoError(t, err) // a cancelled context is a clean shutdown
    case <-time.After(10 * time.Second): t.Fatal("serve did not return after cancel")
    }
}

func TestServe_AddressInUse(t *testing.T) {
    setDB(t)
    taken, err := net.Listen("tcp", "127.0.0.1:0"); require.NoError(t, err); defer taken.Close()
    t.Setenv("HERMES_CHAT_HTTP_ADDR", taken.Addr().String())
    t.Setenv("HERMES_CHAT_DEBUG_ADDR", "127.0.0.1:0")
    cfg, _ := loadConfig()
    done := make(chan error, 1)
    go func() { done <- serve(context.Background(), cfg, newLogger(io.Discard, slog.LevelError), listeners{}) }()
    select {
    case err := <-done: assert.Error(t, err)
    case <-time.After(10 * time.Second): t.Fatal("serve kept running with its address in use")
    }
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/`
Expected: FAIL, undefined `loadConfig`, `run`, `serve`.

- [ ] **Step 3: Implement the seven files** to the behavior listed above.

`go get github.com/quells/grace@v0.1.0`. `main` is `os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))`. Dispatch on `args[0]` with a `switch`; no CLI framework. `admin.go` holds `migrate` here and gains the `user` commands in Task 7.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/hermes-chat/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Run it by hand**

```bash
export HERMES_CHAT_DB_PATH=/tmp/hermes-stage1.db
go run ./cmd/hermes-chat serve &
sleep 1
curl -si localhost:8080/healthz
curl -s -o /dev/null -w '%{http_code}\n' localhost:6060/debug/pprof/
kill -INT %1; wait %1; echo "exit $?"
```

Expected: `HTTP/1.1 200 OK` with an `X-Request-Id` header and body `{"status":"ok"}`; then `200`; JSON log lines on stderr including one `"msg":"request"` with `"route":"/healthz"`; then `exit 0`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd/hermes-chat
git commit -m "Add config, wiring, routes, and serve and migrate commands"
```

---

### Task 7: Command: `user add` and `user list`

**Files:**
- Modify: `cmd/hermes-chat/main.go` (dispatch), `cmd/hermes-chat/admin.go`
- Test: `cmd/hermes-chat/admin_test.go`

**Interfaces:**
- Consumes: `run`, `wireUp(…, wireCheck)`, `wires.Svc`, `bus.Service.CreateHuman`, `bus.Service.ListHumans`, `bus.ErrConflict`, `bus.ErrValidation`, `storage.ErrSchemaBehind`; test helpers `setDB`, `runCmd` from Task 6.
- Produces: subcommands `user add <username>` and `user list`.

Exact output:
- `user add kai` → stdout `created user kai (<ULID>)`, exit 0.
- `user list` → stdout one line per human, ordered by username: `<ULID>\t<username>\t<created-at RFC 3339 UTC>`. No humans: no output, exit 0.
- Schema behind → stderr `hermes-chat: database schema is out of date; run "hermes-chat migrate"`, exit 1.
- Duplicate → stderr `hermes-chat: user "kai" already exists`, exit 1.
- Validation → stderr `hermes-chat: <the bus's validation message>`, exit 1.
- `user`, `user add` with no username, `user add a b`, `user frob` → usage on stderr, exit 2.

- [ ] **Step 1: Write the failing tests** (`package main`)

```go
func migrated(t *testing.T) string { p := setDB(t); c, _, _ := runCmd(t, "migrate"); require.Equal(t, 0, c); return p }

func TestUserAddAndList(t *testing.T) {
    migrated(t)
    code, out, _ := runCmd(t, "user", "add", "Kai")
    require.Equal(t, 0, code)
    require.Regexp(t, `^created user kai \([0-9A-HJKMNP-TV-Z]{26}\)\n$`, out)
    runCmd(t, "user", "add", "ada")

    code, out, _ = runCmd(t, "user", "list"); require.Equal(t, 0, code)
    rows := strings.Split(strings.TrimSpace(out), "\n"); require.Len(t, rows, 2)
    ada := strings.Split(rows[0], "\t"); require.Len(t, ada, 3)
    assert.Equal(t, "ada", ada[1])
    _, err := ulid.ParseStrict(ada[0]); assert.NoError(t, err)
    ts, err := time.Parse(time.RFC3339, ada[2]); require.NoError(t, err)
    assert.Equal(t, time.UTC, ts.Location())
    assert.Contains(t, rows[1], "\tkai\t")
}

func TestUserList_Empty(t *testing.T) {
    migrated(t)
    code, out, _ := runCmd(t, "user", "list"); assert.Equal(t, 0, code); assert.Empty(t, out)
}

func TestUserAdd_Duplicate(t *testing.T) {
    migrated(t); runCmd(t, "user", "add", "kai")
    code, out, errOut := runCmd(t, "user", "add", "KAI")
    assert.Equal(t, 1, code); assert.Empty(t, out)
    assert.Contains(t, errOut, `user "kai" already exists`)
}

func TestUserAdd_Invalid(t *testing.T) {
    migrated(t)
    code, _, errOut := runCmd(t, "user", "add", "kai wells")
    assert.Equal(t, 1, code); assert.Contains(t, errOut, "username")
}

// Review Focus 4.
func TestUserAdd_UnmigratedDatabase(t *testing.T) {
    path := setDB(t)
    code, _, errOut := runCmd(t, "user", "add", "kai")
    assert.Equal(t, 1, code)
    assert.Contains(t, errOut, `run "hermes-chat migrate"`)
    db, err := storage.Open(context.Background(), path); require.NoError(t, err); defer db.Close()
    assert.ErrorIs(t, db.CheckSchema(context.Background()), storage.ErrSchemaBehind) // still not migrated
}

func TestUser_UsageErrors(t *testing.T) {
    migrated(t)
    for _, args := range [][]string{{"user"}, {"user", "add"}, {"user", "add", "a", "b"}, {"user", "frob"}} {
        code, _, errOut := runCmd(t, args...)
        assert.Equal(t, 2, code, args); assert.NotEmpty(t, errOut, args)
    }
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/ -run 'TestUser'`
Expected: FAIL, `user` is an unknown subcommand (exit 2 where 0 or 1 is expected).

- [ ] **Step 3: Implement the `user` commands in `admin.go`** and add them to the dispatch and the usage text. Both use `wireUp(…, wireCheck)` and call `wires.Svc`; they start no runners and build no gin engine.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/hermes-chat/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat
git commit -m "Add user add and user list commands"
```

---

### Task 8: Layering test, README, and the cross-compile check

**Files:**
- Create: `cmd/hermes-chat/layering.go`, `README.md`
- Test: `cmd/hermes-chat/layering_test.go`

**Interfaces:**
- Produces (in `package main`):
  ```go
  type pkgImports struct {
      ImportPath string
      Imports    []string // non-test imports only
  }
  // checkLayering returns one message per forbidden import; empty means the rules hold.
  func checkLayering(pkgs []pkgImports) []string
  ```

Rules, with `I` = `github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal`. "X" below means X and every package under it.

| Package | Must not import |
|---|---|
| `I/app` | `I/storage` |
| `I/bus` | `I/app`, `I/storage/sqlcgen`, `github.com/gin-gonic/gin`, `net/http` |
| `I/storage` | `I/bus`, `I/app`, `github.com/gin-gonic/gin` |
| `I/reqid` | `I/app`, `I/bus`, `I/storage` |
| anything outside `I/storage` | `I/storage/sqlcgen` |

Only non-test imports are checked: `app` tests legitimately build a real repository.

- [ ] **Step 1: Write the failing tests**

```go
const I = "github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal"

func TestCheckLayering_Violations(t *testing.T) {
    cases := []pkgImports{
        {I + "/app", []string{I + "/storage"}},
        {I + "/app", []string{I + "/storage/sqlcgen"}},
        {I + "/bus", []string{I + "/app"}},
        {I + "/bus", []string{"github.com/gin-gonic/gin"}},
        {I + "/bus", []string{"net/http"}},
        {I + "/bus", []string{I + "/storage/sqlcgen"}},
        {I + "/storage", []string{I + "/bus"}},
        {I + "/storage/storagetest", []string{I + "/app"}},
        {I + "/reqid", []string{I + "/bus"}},
        {"github.com/quells-bot/hermes-chat/cmd/hermes-chat", []string{I + "/storage/sqlcgen"}},
    }
    for _, c := range cases {
        assert.NotEmpty(t, checkLayering([]pkgImports{c}), "%s -> %v", c.ImportPath, c.Imports)
    }
}

func TestCheckLayering_Allowed(t *testing.T) {
    ok := []pkgImports{
        {I + "/app", []string{I + "/bus", I + "/reqid", "github.com/gin-gonic/gin", "net/http"}},
        {I + "/bus", []string{I + "/storage", I + "/reqid"}},
        {I + "/storage", []string{I + "/storage/sqlcgen", I + "/reqid"}},
        {"github.com/quells-bot/hermes-chat/cmd/hermes-chat", []string{I + "/app", I + "/bus", I + "/storage"}},
    }
    assert.Empty(t, checkLayering(ok))
}

// The real import graph, from `go list -json ./cmd/hermes-chat/...` run at the module root.
func TestLayering_ThisRepository(t *testing.T) {
    pkgs := listPackages(t) // exec go list -json, decode the stream, keep ImportPath and Imports
    require.GreaterOrEqual(t, len(pkgs), 6)
    assert.Empty(t, checkLayering(pkgs))
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/ -run Layering`
Expected: FAIL, undefined `checkLayering`.

- [ ] **Step 3: Implement `checkLayering` in `layering.go`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/hermes-chat/ -run Layering`
Expected: PASS.

- [ ] **Step 5: Write `README.md`**

Contents, in this order: one paragraph on what the project is, pointing at the two specs; the tool versions table from section 11 of the architecture spec, noting that `just` and `sqlc` must be installed and that goose's CLI is not needed; the `just` recipes and what each does; the environment variables (`HERMES_CHAT_HTTP_ADDR`, `HERMES_CHAT_DEBUG_ADDR`, `HERMES_CHAT_DB_PATH`, `HERMES_CHAT_LOG_LEVEL`, `HERMES_CHAT_GIN_MODE`) with defaults; the subcommands (`serve`, `migrate`, `user add`, `user list`); how to add a migration (next number, `-- +goose Up` / `-- +goose Down`, `STRICT`, then `just generate`); and the layering rule in two sentences.

- [ ] **Step 6: Run the full check and the cross-compiles**

```bash
just check
just build linux amd64
just build linux arm64
just build darwin arm64
file bin/hermes-chat-linux-amd64
```

Expected: `just check` passes with `sqlc diff` clean; three binaries in `bin/`; `file` reports `ELF 64-bit LSB executable, x86-64, … statically linked`.

- [ ] **Step 7: Commit**

```bash
git add README.md cmd/hermes-chat/layering.go cmd/hermes-chat/layering_test.go
git commit -m "Add layering test and README"
```
