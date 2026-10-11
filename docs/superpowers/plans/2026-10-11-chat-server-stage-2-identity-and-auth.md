# Chat Server Stage 2 (Identity and Auth) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Humans log in with a password and get a session cookie, agents authenticate with a bearer token, the admin CLI manages both, and every bus error reaches the client as the right HTTP status.

**Architecture:** Same three layers as stage 1. `storage` gains agents and sessions and a password column; `bus` owns every auth decision (argon2id, token hashing, session expiry, login throttling) and hands `app` an `Actor`; `app` gains one middleware per flavor of caller, a same-origin check, `writeError`, and four small handlers. The admin CLI stays a second front end onto `bus`.

**Tech Stack:** As stage 1, plus `golang.org/x/crypto/argon2`, `golang.org/x/time/rate`, `golang.org/x/term`.

**Spec:** `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (stage 2 of its section 14; sections 4.3, 4.4, 6.1, 6.2, 9 especially). Product rules: section 7 of `docs/superpowers/specs/2026-10-11-hermes-chat-design.md`. Read both before starting any task. Stage 1's plan, `2026-10-11-chat-server-stage-1-skeleton.md`, shows the test and commit style to match.

## Global Constraints

Everything in stage 1's Global Constraints still holds (layering, ULIDs, `STRICT`, integer-millisecond timestamps, temp-file database per test, black-box tests, mocks only to force errors, `…Context` logging in `bus` and `storage`, `CGO_ENABLED=0`). Added for this stage:

- Passwords: argon2id, `m=19456` KiB, `t=2`, `p=1`, 16-byte salt, 32-byte key, stored as a PHC string `$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>` (unpadded standard base64). A password is at least 10 characters and at most 256 bytes.
- Agent tokens and session tokens: 32 bytes from `crypto/rand`, unpadded URL-safe base64, prefixed `hca_` (agent) or `hcs_` (session). **Only the lowercase-hex SHA-256 of the whole token string is stored.** A raw token appears in exactly two places: the CLI output that creates it, and the `Set-Cookie` header.
- Passwords, tokens, cookies, the `Authorization` header, and request bodies are never logged and never appear in an error message.
- A transaction callback does database work only (spec 7.2). Password hashing happens before `InTx`, never inside it.
- The bus never returns a storage sentinel. `app` never sends a bus error's text to the client, except a `ValidationError`'s message.
- Session lifetime: 90 days from login, fixed, no sliding renewal *(this plan's call)*.
- Login throttle *(this plan's numbers)*: per username, burst 5, one more per minute; per IP, burst 20, one more per 10 seconds. Every attempt counts, successful or not.
- Routes *(this plan's call)*: public `GET /healthz`, `POST /api/login`; human API under `/api`; agent API under `/agent`.
- Not in this stage: rooms, the hub, SSE, the heartbeat token re-check, "one live stream per agent", TLS, a background job for expired sessions, any web UI.

Known limit, accepted for now: anyone who knows a username can keep that username's throttle bucket empty and so block its owner from logging in. An existing session is unaffected.

Known defect, accepted by Kai for now: when a handler panics, gin's default recovery middleware dumps the request headers to `gin.DefaultErrorWriter`. It masks `Authorization` but not `Cookie`, so a panic on a human request can write the session cookie to stderr. This is the one exception to the never-logged rule above. Do not rewrite the recovery handler in this stage, and do not add a test that asserts the cookie is absent from recovery output.

## Review Focus

1. **A spoofed `X-Forwarded-For` header.** gin trusts every proxy by default, which would let one client pick a new "IP" per request and walk around the per-IP throttle. The header must be ignored unless the peer is a configured trusted proxy. Pinned in Task 6.
2. **Login attempts with thousands of distinct usernames or IPs.** The throttle's in-memory maps must not grow without bound. Pinned in Task 4.
3. **A human with no password, an empty password, or a huge one.** Humans created in stage 1 have an empty hash and must never be able to log in, including with an empty password; a password over 256 bytes must be refused without being hashed. Pinned in Tasks 2 and 4.
4. **A state-changing request with a missing, `null`, or other-site `Origin`.** It must get 403 before anything else happens; a `GET` with no `Origin` must still work. Pinned in Task 5.
5. **A session that should be dead.** After `user passwd`, `user remove`, logout, or 90 days, the old cookie must stop working on the very next request. Pinned in Tasks 2, 4, and 6.

## File Structure

```
cmd/hermes-chat/
    main.go                        run() and command gain stdin; agent command added
    admin.go                       user add|passwd|remove|list (migrate stays here)
    admin_agent.go                 agent add|revoke|rotate|list              (new)
    password_input.go              readPassword()                            (new)
    config.go                      CookieSecure, TrustedProxies
    wires.go                       passes app.Options
    routes.go                      the three groups
    serve.go                       SetTrustedProxies
    internal/
        storage/
            migrations/00002_auth.sql                                        (new)
            queries/humans.sql, agents.sql (new), sessions.sql (new)
            storage.go             Repository grows
            human.go, agent.go (new), session.go (new)
        bus/
            bus.go                 Service grows; new sentinels
            human.go               passwords on create; passwd; remove; get
            password.go            hashPassword, verifyPassword              (new)
            token.go               newToken, hashToken                       (new)
            actor.go               Actor, ActorKind                          (new)
            agent.go               agents and ResolveAgentToken              (new)
            session.go             Login, Logout, ResolveSession             (new)
            throttle.go            the two limiter maps                      (new)
            export_test.go         test-only accessors                       (new)
        app/
            app.go                 Options; Controller grows
            errors.go              writeError                                (new)
            auth.go                HumanAuth, AgentAuth, SameOrigin, Login, Logout, Me, AgentMe (new)
            middleware.go          RequestLogger adds the actor
            export_test.go         WriteError                                (new)
```

---

### Task 1: Storage: auth schema and repository methods

**Files:**
- Create: `cmd/hermes-chat/internal/storage/migrations/00002_auth.sql`, `queries/agents.sql`, `queries/sessions.sql`, `agent.go`, `session.go`
- Modify: `queries/humans.sql`, `storage.go`, `human.go`, `sqlcgen/` (regenerated)
- Test: `cmd/hermes-chat/internal/storage/storage_test.go` (humans), `agent_test.go`, `session_test.go`

**Interfaces:**
- Consumes: stage 1's `repo`, `mapErr`, `storagetest.NewRepo`, `storagetest.Open`, `(*DB).Pools`.
- Produces:
  ```go
  type Human struct {
      ID, Username string
      PasswordHash string // "" means no password: this human cannot log in
      CreatedAtMs  int64
  }
  type Agent struct {
      ID, Handle, DisplayName, TokenHash string
      RevokedAtMs *int64 // nil while live
      CreatedAtMs int64
  }
  type Session struct {
      ID, TokenHash, HumanID   string
      CreatedAtMs, ExpiresAtMs int64
  }

  // added to Repository
  GetHumanByID(ctx context.Context, id string) (Human, error)             // ErrNotFound
  SetHumanPasswordHash(ctx context.Context, id, hash string) error        // ErrNotFound
  DeleteHuman(ctx context.Context, id string) error                       // ErrNotFound; that human's sessions go with it

  PutAgent(ctx context.Context, a Agent) error                            // ErrConflict on duplicate id, handle, or token hash
  GetAgentByID(ctx context.Context, id string) (Agent, error)             // ErrNotFound
  GetAgentByHandle(ctx context.Context, handle string) (Agent, error)     // ErrNotFound
  GetAgentByTokenHash(ctx context.Context, hash string) (Agent, error)    // ErrNotFound; revoked agents are returned, the bus decides
  ListAgents(ctx context.Context) ([]Agent, error)                        // ordered by handle; empty, not nil
  SetAgentTokenHash(ctx context.Context, id, hash string) error           // ErrNotFound; ErrConflict
  SetAgentRevokedAt(ctx context.Context, id string, revokedAtMs *int64) error // ErrNotFound; nil clears

  PutSession(ctx context.Context, s Session) error                        // ErrConflict on duplicate id or token hash
  GetSessionByTokenHash(ctx context.Context, hash string) (Session, error) // ErrNotFound; expired rows are returned, the bus decides
  DeleteSessionByTokenHash(ctx context.Context, hash string) error        // nil when there is no such row
  DeleteSessionsByHuman(ctx context.Context, humanID string) error
  DeleteExpiredSessions(ctx context.Context, nowMs int64) error           // rows with expires_at <= nowMs
  ```
  `PutHuman`, `GetHumanByUsername`, and `ListHumans` keep their signatures and now carry `PasswordHash`.

- [ ] **Step 1: Write the migration** `migrations/00002_auth.sql`

sqlc 1.31.1 was checked against exactly this file on 2026-10-11.

```sql
-- +goose Up
ALTER TABLE humans ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE agents (
    id           TEXT    NOT NULL PRIMARY KEY,
    handle       TEXT    NOT NULL UNIQUE,
    display_name TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    revoked_at   INTEGER,
    created_at   INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    id         TEXT    NOT NULL PRIMARY KEY,
    token_hash TEXT    NOT NULL UNIQUE,
    human_id   TEXT    NOT NULL REFERENCES humans (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_human_id ON sessions (human_id);

-- +goose Down
DROP TABLE sessions;
DROP TABLE agents;
ALTER TABLE humans DROP COLUMN password_hash;
```

- [ ] **Step 2: Write the failing tests** (package `storage_test`; `ctx`, `kai`, `ada` already exist in `storage_test.go`)

```go
// storage_test.go
func TestHuman_PasswordHashRoundTrip(t *testing.T) {
    r := storagetest.NewRepo(t)
    h := kai; h.PasswordHash = "$argon2id$stub"
    require.NoError(t, r.PutHuman(ctx, h))
    byName, err := r.GetHumanByUsername(ctx, "kai"); require.NoError(t, err); assert.Equal(t, h, byName)
    byID, err := r.GetHumanByID(ctx, kai.ID); require.NoError(t, err); assert.Equal(t, h, byID)
    all, _ := r.ListHumans(ctx); assert.Equal(t, []storage.Human{h}, all)
    _, err = r.GetHumanByID(ctx, ada.ID); assert.ErrorIs(t, err, storage.ErrNotFound)
}

// A row written before this migration existed has an empty hash.
func TestHuman_RowWithoutPasswordColumnHasEmptyHash(t *testing.T) {
    db := storagetest.Open(t); _, write := db.Pools()
    _, err := write.ExecContext(ctx, `INSERT INTO humans (id, username, created_at) VALUES (?, 'kai', 1)`, kai.ID)
    require.NoError(t, err)
    got, err := storage.NewRepo(db).GetHumanByUsername(ctx, "kai"); require.NoError(t, err)
    assert.Equal(t, "", got.PasswordHash)
}

func TestSetHumanPasswordHash(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutHuman(ctx, kai))
    require.NoError(t, r.SetHumanPasswordHash(ctx, kai.ID, "h2"))
    got, _ := r.GetHumanByID(ctx, kai.ID); assert.Equal(t, "h2", got.PasswordHash)
    assert.ErrorIs(t, r.SetHumanPasswordHash(ctx, ada.ID, "h"), storage.ErrNotFound)
}

func TestDeleteHuman_TakesSessionsWithIt(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutHuman(ctx, kai)); require.NoError(t, r.PutHuman(ctx, ada))
    require.NoError(t, r.PutSession(ctx, sess("1", kai.ID, 100)))
    require.NoError(t, r.PutSession(ctx, sess("2", ada.ID, 100)))
    require.NoError(t, r.DeleteHuman(ctx, kai.ID))
    _, err := r.GetHumanByID(ctx, kai.ID);          assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "hash-1"); assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "hash-2"); assert.NoError(t, err)
    assert.ErrorIs(t, r.DeleteHuman(ctx, kai.ID), storage.ErrNotFound)
}

// agent_test.go
func ms(v int64) *int64 { return &v }
var scout = storage.Agent{ID: "01JA0000000000000000000011", Handle: "scout", DisplayName: "Scout", TokenHash: "hash-scout", CreatedAtMs: 1791700000000}
var atlas = storage.Agent{ID: "01JA0000000000000000000012", Handle: "atlas", DisplayName: "Atlas", TokenHash: "hash-atlas", CreatedAtMs: 1791700000001}

func TestAgent_PutAndGetThreeWays(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutAgent(ctx, scout))
    for _, get := range []func() (storage.Agent, error){
        func() (storage.Agent, error) { return r.GetAgentByID(ctx, scout.ID) },
        func() (storage.Agent, error) { return r.GetAgentByHandle(ctx, "scout") },
        func() (storage.Agent, error) { return r.GetAgentByTokenHash(ctx, "hash-scout") },
    } {
        got, err := get(); require.NoError(t, err); assert.Equal(t, scout, got) // RevokedAtMs is nil
    }
    _, err := r.GetAgentByID(ctx, atlas.ID);            assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetAgentByHandle(ctx, "atlas");          assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetAgentByTokenHash(ctx, "hash-atlas");  assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestAgent_Conflicts(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutAgent(ctx, scout)); require.NoError(t, r.PutAgent(ctx, atlas))
    dup := func(mut func(*storage.Agent)) error {
        a := storage.Agent{ID: "01JA0000000000000000000013", Handle: "new", DisplayName: "New", TokenHash: "hash-new"}
        mut(&a); return r.PutAgent(ctx, a)
    }
    assert.ErrorIs(t, dup(func(a *storage.Agent) { a.ID = scout.ID }), storage.ErrConflict)
    assert.ErrorIs(t, dup(func(a *storage.Agent) { a.Handle = "scout" }), storage.ErrConflict)
    assert.ErrorIs(t, dup(func(a *storage.Agent) { a.TokenHash = "hash-scout" }), storage.ErrConflict)
    assert.ErrorIs(t, r.SetAgentTokenHash(ctx, atlas.ID, "hash-scout"), storage.ErrConflict)
}

func TestListAgents(t *testing.T) {
    r := storagetest.NewRepo(t)
    got, err := r.ListAgents(ctx); require.NoError(t, err); assert.NotNil(t, got); assert.Empty(t, got)
    require.NoError(t, r.PutAgent(ctx, scout)); require.NoError(t, r.PutAgent(ctx, atlas))
    got, _ = r.ListAgents(ctx); assert.Equal(t, []storage.Agent{atlas, scout}, got)
}

func TestAgent_SetTokenHashAndRevokedAt(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutAgent(ctx, scout))
    require.NoError(t, r.SetAgentTokenHash(ctx, scout.ID, "hash-2"))
    require.NoError(t, r.SetAgentRevokedAt(ctx, scout.ID, ms(42)))
    got, err := r.GetAgentByTokenHash(ctx, "hash-2"); require.NoError(t, err) // revoked agents are still returned
    require.NotNil(t, got.RevokedAtMs); assert.EqualValues(t, 42, *got.RevokedAtMs)
    _, err = r.GetAgentByTokenHash(ctx, "hash-scout"); assert.ErrorIs(t, err, storage.ErrNotFound)
    require.NoError(t, r.SetAgentRevokedAt(ctx, scout.ID, nil))
    got, _ = r.GetAgentByID(ctx, scout.ID); assert.Nil(t, got.RevokedAtMs)
    assert.ErrorIs(t, r.SetAgentTokenHash(ctx, atlas.ID, "x"), storage.ErrNotFound)
    assert.ErrorIs(t, r.SetAgentRevokedAt(ctx, atlas.ID, ms(1)), storage.ErrNotFound)
}

// session_test.go
// sess builds a session whose ID and token hash are derived from n.
func sess(n, humanID string, expiresAtMs int64) storage.Session {
    return storage.Session{ID: "01JA00000000000000000000S" + n, TokenHash: "hash-" + n, HumanID: humanID, CreatedAtMs: 1, ExpiresAtMs: expiresAtMs}
}

func TestSession_PutGetDelete(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutHuman(ctx, kai))
    s := sess("1", kai.ID, 100); require.NoError(t, r.PutSession(ctx, s))
    got, err := r.GetSessionByTokenHash(ctx, "hash-1"); require.NoError(t, err); assert.Equal(t, s, got)
    again := sess("2", kai.ID, 100); again.TokenHash = "hash-1"
    assert.ErrorIs(t, r.PutSession(ctx, again), storage.ErrConflict)
    require.NoError(t, r.DeleteSessionByTokenHash(ctx, "hash-1"))
    _, err = r.GetSessionByTokenHash(ctx, "hash-1"); assert.ErrorIs(t, err, storage.ErrNotFound)
    assert.NoError(t, r.DeleteSessionByTokenHash(ctx, "hash-1")) // absent is not an error
}

func TestSession_UnknownHumanIsRejected(t *testing.T) { // foreign keys are on
    assert.Error(t, storagetest.NewRepo(t).PutSession(ctx, sess("1", kai.ID, 100)))
}

func TestDeleteSessionsByHuman(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutHuman(ctx, kai)); require.NoError(t, r.PutHuman(ctx, ada))
    for _, s := range []storage.Session{sess("1", kai.ID, 100), sess("2", kai.ID, 100), sess("3", ada.ID, 100)} {
        require.NoError(t, r.PutSession(ctx, s))
    }
    require.NoError(t, r.DeleteSessionsByHuman(ctx, kai.ID))
    _, err := r.GetSessionByTokenHash(ctx, "hash-1"); assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "hash-2");  assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "hash-3");  assert.NoError(t, err)
}

func TestDeleteExpiredSessions_BoundaryIsInclusive(t *testing.T) {
    r := storagetest.NewRepo(t); require.NoError(t, r.PutHuman(ctx, kai))
    require.NoError(t, r.PutSession(ctx, sess("1", kai.ID, 99)))
    require.NoError(t, r.PutSession(ctx, sess("2", kai.ID, 100)))
    require.NoError(t, r.PutSession(ctx, sess("3", kai.ID, 101)))
    require.NoError(t, r.DeleteExpiredSessions(ctx, 100))
    _, err := r.GetSessionByTokenHash(ctx, "hash-1"); assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "hash-2");  assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "hash-3");  assert.NoError(t, err)
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/storage/...`
Expected: FAIL, `storage.Agent` and the new methods are undefined.

- [ ] **Step 4: Write the queries and generate**

One query per repository method, named for it. Use `:execrows` for every `UPDATE` and for `DeleteHuman`, so zero rows affected becomes `ErrNotFound`; `:exec` for the three session deletes. Select columns by name, never `*`.

Run: `just generate`
Expected: `sqlcgen/agents.sql.go` and `sessions.sql.go` appear; `models.go` gains `Agent` and `Session`; `sqlcgen.Agent.RevokedAt` is `sql.NullInt64`.

- [ ] **Step 5: Implement the repository methods**

Reads go through `r.reads`, writes through `r.writes`, every driver error through `mapErr`. `sql.NullInt64` is converted to and from `*int64` inside `agent.go` and never leaves the package. A foreign-key failure is left unmapped.

- [ ] **Step 6: Run to verify pass**

Run: `go test ./cmd/hermes-chat/internal/storage/... && just check`
Expected: PASS; `sqlc diff` clean. (The other packages still compile: nothing they call changed shape.)

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "Add agents, sessions, and password hash to storage"
```

---

### Task 2: Humans with passwords: bus and `user add | passwd | remove`

**Files:**
- Create: `cmd/hermes-chat/internal/bus/password.go`, `bus/export_test.go`, `cmd/hermes-chat/password_input.go`
- Modify: `bus/bus.go`, `bus/human.go`, `cmd/hermes-chat/main.go`, `admin.go`
- Test: `bus/password_test.go`, `bus/bus_test.go`, `cmd/hermes-chat/admin_test.go`, `main_test.go`

**Interfaces:**
- Consumes: Task 1's `GetHumanByID`, `SetHumanPasswordHash`, `DeleteHuman`, `DeleteSessionsByHuman`, `PutSession`, `GetSessionByTokenHash`.
- Produces:
  ```go
  package bus
  // Service: CreateHuman changes, three methods are added
  CreateHuman(ctx context.Context, username, password string) (Human, error) // ErrValidation, ErrConflict
  SetHumanPassword(ctx context.Context, username, password string) error     // ErrNotFound, ErrValidation; ends all that human's sessions
  RemoveHuman(ctx context.Context, username string) error                    // ErrNotFound
  GetHuman(ctx context.Context, id ulid.ULID) (Human, error)                 // ErrNotFound

  // unexported, in password.go
  func hashPassword(password string) (string, error)
  func verifyPassword(encoded, password string) bool // false for "" or anything unparseable

  // export_test.go
  var HashPassword, VerifyPassword = hashPassword, verifyPassword

  package main
  type command func(ctx context.Context, cfg Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int
  func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int
  func readPassword(stdin io.Reader, stderr io.Writer) (string, error)
  // test helpers
  func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string)            // empty stdin
  func runCmdIn(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string)
  ```

Rules pinned by this task:
- Password rule and exact message: `password must be at least 10 characters and at most 256 bytes`. Username is validated first, then the password.
- `username` arguments to `SetHumanPassword` and `RemoveHuman` are trimmed and lowercased like `CreateHuman`'s; one that does not match the pattern is `ErrNotFound`.
- `SetHumanPassword` hashes first, then in one `InTx` sets the hash and deletes the human's sessions.
- `verifyPassword` reads `m`, `t`, `p` from the stored string (so the parameters can be raised later without locking anyone out) and compares with `subtle.ConstantTimeCompare`.
- `readPassword`: if `stdin` is an `*os.File` that `term.IsTerminal` accepts, prompt `Password: ` then `Repeat password: ` on `stderr` with `term.ReadPassword`, and fail with `passwords do not match` if they differ. Otherwise read the first line of `stdin` and strip the trailing `\n` or `\r\n`. An empty result is passed on; the bus rejects it.
- `user add` and `user passwd` wire up (and so check the schema) before reading the password, so an unmigrated database is reported without prompting.
- CLI output: `user add` unchanged (`created user kai (<ULID>)`); `user passwd kai` → `password updated for kai`; `user remove kai` → `removed user kai`; unknown user → stderr `hermes-chat: user "nobody" not found`, exit 1. `user passwd` and `user remove` take exactly one argument; anything else is usage, exit 2.

- [ ] **Step 1: Write the failing tests**

```go
// bus/password_test.go (package bus_test)
const pw = "correct horse battery"

func TestHashPassword_FormatAndVerify(t *testing.T) {
    h, err := bus.HashPassword(pw); require.NoError(t, err)
    assert.Regexp(t, `^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, h)
    assert.True(t, bus.VerifyPassword(h, pw))
    assert.False(t, bus.VerifyPassword(h, pw+"x"))
    assert.False(t, bus.VerifyPassword(h, ""))
    h2, _ := bus.HashPassword(pw); assert.NotEqual(t, h, h2) // fresh salt each time
}

func TestVerifyPassword_HonorsStoredParameters(t *testing.T) {
    salt := []byte("saltsaltsaltsalt"); b64 := base64.RawStdEncoding.EncodeToString
    weak := "$argon2id$v=19$m=8,t=1,p=1$" + b64(salt) + "$" + b64(argon2.IDKey([]byte(pw), salt, 1, 8, 1, 32))
    assert.True(t, bus.VerifyPassword(weak, pw)); assert.False(t, bus.VerifyPassword(weak, pw+"x"))
}

func TestVerifyPassword_GarbageIsFalseNotPanic(t *testing.T) { // Review Focus 3
    for _, bad := range []string{"", "plain", "$argon2id$", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=x,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=8,t=1,p=1$!!!$a2V5", "$argon2id$v=19$m=8,t=0,p=0$c2FsdA$a2V5"} {
        assert.False(t, bus.VerifyPassword(bad, ""), "%q", bad)
        assert.False(t, bus.VerifyPassword(bad, pw), "%q", bad)
    }
}

// bus/bus_test.go: every existing CreateHuman(ctx, name) call gains pw as its last argument. Then add:
func newSvcRepo(t *testing.T) (bus.Service, storage.Repository) // like newSvc, also returning the repository under it

func TestCreateHuman_StoresAHashNotThePassword(t *testing.T) {
    s, r := newSvcRepo(t); _, err := s.CreateHuman(ctx, "kai", pw); require.NoError(t, err)
    row, _ := r.GetHumanByUsername(ctx, "kai")
    assert.NotContains(t, row.PasswordHash, pw)
    assert.True(t, bus.VerifyPassword(row.PasswordHash, pw))
}

func TestCreateHuman_PasswordValidation(t *testing.T) {
    s := newSvc(t)
    for _, bad := range []string{"", "123456789", strings.Repeat("a", 257)} {
        _, err := s.CreateHuman(ctx, "kai", bad)
        var ve bus.ValidationError; require.ErrorAs(t, err, &ve, "%q", bad)
        assert.Equal(t, "password must be at least 10 characters and at most 256 bytes", ve.Message)
    }
    _, err := s.CreateHuman(ctx, "ada", "ünïcödé-pw"); assert.NoError(t, err)          // 10 characters, 14 bytes
    _, err = s.CreateHuman(ctx, "bob", strings.Repeat("a", 256)); assert.NoError(t, err)
    _, err = s.CreateHuman(ctx, "bad name", ""); var ve bus.ValidationError
    require.ErrorAs(t, err, &ve); assert.Contains(t, ve.Message, "username") // username is checked first
}

func TestGetHuman(t *testing.T) {
    s := newSvc(t); h, _ := s.CreateHuman(ctx, "kai", pw)
    got, err := s.GetHuman(ctx, h.ID); require.NoError(t, err); assert.Equal(t, h, got)
    _, err = s.GetHuman(ctx, ulid.Make()); assert.ErrorIs(t, err, bus.ErrNotFound)
    assert.NotErrorIs(t, err, storage.ErrNotFound)
}

// Review Focus 5.
func TestSetHumanPassword_ReplacesHashAndEndsSessions(t *testing.T) {
    s, r := newSvcRepo(t); kai, _ := s.CreateHuman(ctx, "kai", pw); ada, _ := s.CreateHuman(ctx, "ada", pw)
    put := func(n string, h bus.Human) { require.NoError(t, r.PutSession(ctx, storage.Session{ID: ulid.Make().String(), TokenHash: n, HumanID: h.ID.String(), ExpiresAtMs: 1 << 60})) }
    put("k", kai); put("a", ada)
    require.NoError(t, s.SetHumanPassword(ctx, " KAI ", "a whole new password"))
    row, _ := r.GetHumanByUsername(ctx, "kai")
    assert.True(t, bus.VerifyPassword(row.PasswordHash, "a whole new password")); assert.False(t, bus.VerifyPassword(row.PasswordHash, pw))
    _, err := r.GetSessionByTokenHash(ctx, "k"); assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetSessionByTokenHash(ctx, "a"); assert.NoError(t, err) // ada is untouched
}

func TestSetHumanPassword_Errors(t *testing.T) {
    s, r := newSvcRepo(t); _, _ = s.CreateHuman(ctx, "kai", pw)
    assert.ErrorIs(t, s.SetHumanPassword(ctx, "nobody", pw), bus.ErrNotFound)
    assert.ErrorIs(t, s.SetHumanPassword(ctx, "not a name", pw), bus.ErrNotFound)
    assert.ErrorIs(t, s.SetHumanPassword(ctx, "kai", "short"), bus.ErrValidation)
    row, _ := r.GetHumanByUsername(ctx, "kai"); assert.True(t, bus.VerifyPassword(row.PasswordHash, pw)) // unchanged
}

func TestRemoveHuman(t *testing.T) {
    s := newSvc(t); _, _ = s.CreateHuman(ctx, "kai", pw)
    require.NoError(t, s.RemoveHuman(ctx, "Kai"))
    got, _ := s.ListHumans(ctx); assert.Empty(t, got)
    assert.ErrorIs(t, s.RemoveHuman(ctx, "kai"), bus.ErrNotFound)
    _, err := s.CreateHuman(ctx, "kai", pw); assert.NoError(t, err) // the name is free again
}

// cmd/hermes-chat/admin_test.go: existing `user add` calls become runCmdIn(t, pw+"\n", "user", "add", …). Then add:
const pw = "correct horse battery"

func TestUserAdd_ReadsPasswordFromStdin(t *testing.T) {
    path := migrated(t)
    code, out, errOut := runCmdIn(t, pw+"\r\n", "user", "add", "kai")
    require.Equal(t, 0, code, errOut); assert.NotContains(t, out+errOut, pw)
    assert.True(t, storedPasswordMatches(t, path, "kai", pw))
}

func TestUserAdd_NoOrShortPassword(t *testing.T) {
    migrated(t)
    for _, in := range []string{"", "\n", "short\n"} {
        code, out, errOut := runCmdIn(t, in, "user", "add", "kai")
        assert.Equal(t, 1, code); assert.Empty(t, out)
        assert.Equal(t, "hermes-chat: password must be at least 10 characters and at most 256 bytes\n", errOut)
    }
    _, out, _ := runCmd(t, "user", "list"); assert.Empty(t, out) // nobody was created
}

func TestUserPasswd(t *testing.T) {
    path := migrated(t); runCmdIn(t, pw+"\n", "user", "add", "kai")
    code, out, _ := runCmdIn(t, "another long password\n", "user", "passwd", "KAI")
    assert.Equal(t, 0, code); assert.Equal(t, "password updated for kai\n", out)
    assert.True(t, storedPasswordMatches(t, path, "kai", "another long password"))
    code, _, errOut := runCmdIn(t, pw+"\n", "user", "passwd", "nobody")
    assert.Equal(t, 1, code); assert.Equal(t, "hermes-chat: user \"nobody\" not found\n", errOut)
}

func TestUserRemove(t *testing.T) {
    migrated(t); runCmdIn(t, pw+"\n", "user", "add", "kai")
    code, out, _ := runCmd(t, "user", "remove", "kai")
    assert.Equal(t, 0, code); assert.Equal(t, "removed user kai\n", out)
    code, _, errOut := runCmd(t, "user", "remove", "kai")
    assert.Equal(t, 1, code); assert.Contains(t, errOut, `user "kai" not found`)
}

// extend TestUser_UsageErrors with {"user","passwd"}, {"user","passwd","a","b"}, {"user","remove"}, {"user","remove","a","b"}.
```

`storedPasswordMatches` lives in `admin_test.go`: it opens the database with `storage.Open`, reads the row with `storage.NewRepo(db).GetHumanByUsername`, parses the PHC string, and recomputes the key with `argon2.IDKey`. That keeps the check independent of the code under test.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile: `CreateHuman` takes two arguments, `bus.HashPassword` and `runCmdIn` are undefined.

- [ ] **Step 3: Implement the bus side** (`password.go`, `export_test.go`, `human.go`, the `Service` interface)

`go get golang.org/x/crypto`. Share one `normalizeUsername(s string) (string, bool)` between the four methods that take a username.

- [ ] **Step 4: Implement the CLI side** (`password_input.go`, `main.go`, `admin.go`)

`go get golang.org/x/term`. `main` passes `os.Stdin`. Thread `stdin` through `command`; `serve` and `migrate` ignore it. Update the `user` summary in `commandList` and `userUsage` to list `add`, `passwd`, `remove`, `list`.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Check the terminal path by hand** (the tests cannot reach it)

```bash
export HERMES_CHAT_DB_PATH=/tmp/hermes-stage2.db
go run ./cmd/hermes-chat migrate
go run ./cmd/hermes-chat user add kai     # type two different passwords
go run ./cmd/hermes-chat user add kai     # type the same password twice
```

Expected: both prompts appear and nothing typed is echoed; the first run prints `hermes-chat: passwords do not match` and exits 1; the second prints `created user kai (…)`.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum cmd/hermes-chat
git commit -m "Add passwords for humans and user passwd and remove commands"
```

---

### Task 3: Agents and tokens: bus and `agent add | revoke | rotate | list`

**Files:**
- Create: `cmd/hermes-chat/internal/bus/actor.go`, `token.go`, `agent.go`, `cmd/hermes-chat/admin_agent.go`
- Modify: `bus/bus.go`, `cmd/hermes-chat/main.go`
- Test: `bus/agent_test.go`, `cmd/hermes-chat/admin_agent_test.go`

**Interfaces:**
- Consumes: Task 1's agent repository methods; Task 2's `runCmd`, `migrated`, `checkedWires`.
- Produces:
  ```go
  package bus
  var (
      ErrUnauthenticated = errors.New("unauthenticated")
      ErrForbidden       = errors.New("forbidden")
  )
  type ActorKind string
  const (
      ActorHuman ActorKind = "human"
      ActorAgent ActorKind = "agent"
  )
  // Actor is who is calling. Middleware builds it; handlers pass it to bus methods.
  type Actor struct {
      Kind ActorKind
      ID   ulid.ULID
  }
  func (a Actor) String() string // "human:<ULID>" or "agent:<ULID>"

  type Agent struct {
      ID          ulid.ULID
      Handle      string    // without the @
      DisplayName string
      CreatedAt   time.Time
      RevokedAt   time.Time // zero while live
  }
  func (a Agent) Revoked() bool

  // added to Service
  CreateAgent(ctx context.Context, handle, displayName string) (Agent, string, error) // the token, returned this once
  RotateAgentToken(ctx context.Context, handle string) (string, error)                // ErrNotFound; the old token stops working; a revoked agent is live again
  RevokeAgent(ctx context.Context, handle string) error                               // ErrNotFound; already revoked is a no-op that keeps the first time
  ListAgents(ctx context.Context) ([]Agent, error)                                    // ordered by handle
  GetAgent(ctx context.Context, id ulid.ULID) (Agent, error)                          // ErrNotFound
  ResolveAgentToken(ctx context.Context, token string) (Actor, error)                 // ErrUnauthenticated: empty, unknown, or revoked

  // unexported, in token.go; used again by sessions in Task 4
  func newToken(prefix string) (token, hash string, err error) // prefix + base64.RawURLEncoding of 32 crypto/rand bytes
  func hashToken(token string) string                          // lowercase hex SHA-256 of the whole string
  ```

Rules pinned by this task:
- Handle: trim, strip one leading `@`, lowercase, then the username pattern `^[a-z0-9][a-z0-9_-]{0,31}$`. Message: `handle must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit`. In lookups, a handle that fails the pattern is `ErrNotFound`.
- Display name: trimmed; 1 to 64 characters; no control characters. Message: `display name must be 1-64 characters with no control characters`.
- `RotateAgentToken` sets the new hash and clears the revocation in one `InTx`.
- CLI, exact stdout:
  - `agent add scout "Scout the Researcher"` → `created agent @scout (<ULID>)` then `token: <token>`.
  - `agent rotate scout` → `rotated token for @scout` then `token: <token>`.
  - `agent revoke scout` → `revoked agent @scout`.
  - `agent list` → one line per agent, ordered by handle: `<ULID>\t@<handle>\t<display name>\t<active|revoked>\t<created-at RFC 3339 UTC>`.
  - Duplicate → stderr `hermes-chat: agent "@scout" already exists`; unknown → `hermes-chat: agent "@scout" not found`; both exit 1. Wrong argument count → usage, exit 2.

- [ ] **Step 1: Write the failing tests**

```go
// bus/agent_test.go (package bus_test)
func TestCreateAgent(t *testing.T) {
    s, r := newSvcRepo(t)
    a, token, err := s.CreateAgent(ctx, " @Scout ", "  Scout the Researcher ")
    require.NoError(t, err)
    assert.Equal(t, "scout", a.Handle); assert.Equal(t, "Scout the Researcher", a.DisplayName)
    assert.Equal(t, fixed, a.CreatedAt); assert.False(t, a.Revoked())
    assert.Regexp(t, `^hca_[A-Za-z0-9_-]{43}$`, token)
    row, _ := r.GetAgentByHandle(ctx, "scout")
    sum := sha256.Sum256([]byte(token))
    assert.Equal(t, hex.EncodeToString(sum[:]), row.TokenHash) // only the hash is stored
    _, token2, _ := s.CreateAgent(ctx, "atlas", "Atlas"); assert.NotEqual(t, token, token2)
}

func TestCreateAgent_ValidationAndConflict(t *testing.T) {
    s := newSvc(t)
    for _, bad := range []string{"", "@", "@@scout", "-scout", "sc out", strings.Repeat("a", 33)} {
        _, _, err := s.CreateAgent(ctx, bad, "Scout"); assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
    }
    for _, bad := range []string{"", "   ", "line\nbreak", "tab\tbed", strings.Repeat("é", 65)} {
        _, _, err := s.CreateAgent(ctx, "scout", bad); assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
    }
    _, _, err := s.CreateAgent(ctx, "scout", strings.Repeat("é", 64)); require.NoError(t, err)
    _, _, err = s.CreateAgent(ctx, "@SCOUT", "Other")
    assert.ErrorIs(t, err, bus.ErrConflict); assert.NotErrorIs(t, err, storage.ErrConflict)
}

func TestResolveAgentToken(t *testing.T) {
    s := newSvc(t); a, token, _ := s.CreateAgent(ctx, "scout", "Scout")
    actor, err := s.ResolveAgentToken(ctx, token); require.NoError(t, err)
    assert.Equal(t, bus.Actor{Kind: bus.ActorAgent, ID: a.ID}, actor)
    assert.Equal(t, "agent:"+a.ID.String(), actor.String())
    for _, bad := range []string{"", "hca_", token + "x", token[:len(token)-1], strings.ToUpper(token), " " + token} {
        _, err := s.ResolveAgentToken(ctx, bad); assert.ErrorIs(t, err, bus.ErrUnauthenticated, "%q", bad)
    }
}

func TestRevokeAndRotate(t *testing.T) {
    clock := fixed; s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return clock })
    a, t1, _ := s.CreateAgent(ctx, "scout", "Scout")

    require.NoError(t, s.RevokeAgent(ctx, "@scout"))
    _, err := s.ResolveAgentToken(ctx, t1); assert.ErrorIs(t, err, bus.ErrUnauthenticated)
    got, _ := s.GetAgent(ctx, a.ID); assert.True(t, got.Revoked()); assert.Equal(t, fixed, got.RevokedAt)
    clock = fixed.Add(time.Hour); require.NoError(t, s.RevokeAgent(ctx, "scout"))
    got, _ = s.GetAgent(ctx, a.ID); assert.Equal(t, fixed, got.RevokedAt) // the first revocation time is kept

    t2, err := s.RotateAgentToken(ctx, "scout"); require.NoError(t, err); assert.NotEqual(t, t1, t2)
    _, err = s.ResolveAgentToken(ctx, t1); assert.ErrorIs(t, err, bus.ErrUnauthenticated) // old token is dead
    actor, err := s.ResolveAgentToken(ctx, t2); require.NoError(t, err); assert.Equal(t, a.ID, actor.ID) // rotation un-revokes
    got, _ = s.GetAgent(ctx, a.ID); assert.False(t, got.Revoked())

    assert.ErrorIs(t, s.RevokeAgent(ctx, "nobody"), bus.ErrNotFound)
    _, err = s.RotateAgentToken(ctx, "no body"); assert.ErrorIs(t, err, bus.ErrNotFound)
    _, err = s.GetAgent(ctx, ulid.Make()); assert.ErrorIs(t, err, bus.ErrNotFound)
}

func TestListAgents_OrderedByHandle(t *testing.T) {
    s := newSvc(t); _, _, _ = s.CreateAgent(ctx, "scout", "Scout"); _, _, _ = s.CreateAgent(ctx, "atlas", "Atlas")
    got, err := s.ListAgents(ctx); require.NoError(t, err); require.Len(t, got, 2)
    assert.Equal(t, "atlas", got[0].Handle); assert.Equal(t, "scout", got[1].Handle)
}

func TestHumanActorString(t *testing.T) {
    id := ulid.Make(); assert.Equal(t, "human:"+id.String(), bus.Actor{Kind: bus.ActorHuman, ID: id}.String())
}

// cmd/hermes-chat/admin_agent_test.go (package main)
var tokenLine = regexp.MustCompile(`(?m)^token: (hca_[A-Za-z0-9_-]{43})$`)

func TestAgentAddListRevokeRotate(t *testing.T) {
    migrated(t)
    code, out, errOut := runCmd(t, "agent", "add", "@Scout", "Scout the Researcher")
    require.Equal(t, 0, code, errOut)
    require.Regexp(t, `^created agent @scout \([0-9A-HJKMNP-TV-Z]{26}\)\ntoken: hca_[A-Za-z0-9_-]{43}\n$`, out)
    first := tokenLine.FindStringSubmatch(out)[1]
    runCmd(t, "agent", "add", "atlas", "Atlas")

    _, out, _ = runCmd(t, "agent", "list")
    rows := strings.Split(strings.TrimSpace(out), "\n"); require.Len(t, rows, 2)
    cols := strings.Split(rows[1], "\t"); require.Len(t, cols, 5)
    assert.Equal(t, []string{"@scout", "Scout the Researcher", "active"}, cols[1:4])
    _, err := time.Parse(time.RFC3339, cols[4]); assert.NoError(t, err)
    assert.NotContains(t, out, first) // list never shows a token

    code, out, _ = runCmd(t, "agent", "revoke", "scout")
    assert.Equal(t, 0, code); assert.Equal(t, "revoked agent @scout\n", out)
    _, out, _ = runCmd(t, "agent", "list"); assert.Contains(t, out, "\trevoked\t")

    code, out, _ = runCmd(t, "agent", "rotate", "scout"); assert.Equal(t, 0, code)
    require.Regexp(t, `^rotated token for @scout\ntoken: hca_[A-Za-z0-9_-]{43}\n$`, out)
    assert.NotEqual(t, first, tokenLine.FindStringSubmatch(out)[1])
    _, out, _ = runCmd(t, "agent", "list"); assert.NotContains(t, out, "\trevoked\t")
}

func TestAgent_Errors(t *testing.T) {
    migrated(t); runCmd(t, "agent", "add", "scout", "Scout")
    code, out, errOut := runCmd(t, "agent", "add", "scout", "Again")
    assert.Equal(t, 1, code); assert.Empty(t, out); assert.Equal(t, "hermes-chat: agent \"@scout\" already exists\n", errOut)
    code, _, errOut = runCmd(t, "agent", "revoke", "nobody")
    assert.Equal(t, 1, code); assert.Equal(t, "hermes-chat: agent \"@nobody\" not found\n", errOut)
    code, _, errOut = runCmd(t, "agent", "add", "sc out", "Scout")
    assert.Equal(t, 1, code); assert.Contains(t, errOut, "handle must be")
    for _, args := range [][]string{{"agent"}, {"agent", "add"}, {"agent", "add", "scout"}, {"agent", "add", "a", "b", "c"}, {"agent", "revoke"}, {"agent", "rotate"}, {"agent", "list", "x"}, {"agent", "frob"}} {
        code, _, errOut := runCmd(t, args...); assert.Equal(t, 2, code, args); assert.NotEmpty(t, errOut, args)
    }
}

func TestAgent_UnmigratedDatabase(t *testing.T) {
    setDB(t); code, _, errOut := runCmd(t, "agent", "list")
    assert.Equal(t, 1, code); assert.Contains(t, errOut, `run "hermes-chat migrate"`)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL, `CreateAgent` and `bus.Actor` undefined; `agent` is an unknown command.

- [ ] **Step 3: Implement** `actor.go`, `token.go`, `agent.go`, the `Service` additions, `admin_agent.go`, and the `agent` entry in `commandList` (summary `manage agents (add, revoke, rotate, list)`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat
git commit -m "Add agents, bearer tokens, and the agent admin commands"
```

---

### Task 4: Sessions and throttled login in the bus

**Files:**
- Create: `cmd/hermes-chat/internal/bus/session.go`, `throttle.go`
- Modify: `bus/bus.go` (sentinels, `Service`, the `service` struct), `bus/export_test.go`
- Test: `bus/session_test.go`

**Interfaces:**
- Consumes: `newToken`, `hashToken`, `verifyPassword`, `hashPassword`, `normalizeUsername`, `Actor`; Task 1's session repository methods.
- Produces:
  ```go
  const SessionLifetime = 90 * 24 * time.Hour

  var (
      ErrInvalidCredentials = errors.New("invalid credentials")
      ErrThrottled          = errors.New("throttled")
  )
  // ThrottledError says how long to wait. errors.Is(err, ErrThrottled) holds.
  type ThrottledError struct{ RetryAfter time.Duration }

  type Session struct {
      Token     string    // the cookie value; exists only here and in the browser
      Human     Human
      ExpiresAt time.Time
  }

  // added to Service
  Login(ctx context.Context, username, password, ip string) (Session, error) // ErrInvalidCredentials, ThrottledError
  Logout(ctx context.Context, token string) error                            // an unknown or empty token is not an error
  ResolveSession(ctx context.Context, token string) (Actor, error)           // ErrUnauthenticated: empty, unknown, or expired

  // export_test.go
  func (s *service) ThrottleEntries() (usernames, ips int)
  ```
  `NewService(repo, clock)` keeps its signature; the limiter maps are created inside it.

`Login` does these in this order, and the order is the specification:

1. If a minute or more has passed since the last sweep, delete from both limiter maps every entry whose bucket is full again (`TokensAt(now) >= burst`). A full bucket and no bucket behave the same, so nothing is forgotten.
2. Take one token from the IP's limiter (burst 20, `rate.Every(10*time.Second)`). If it is not available now, give the reservation back and return `ThrottledError` with the wait.
3. Normalize the username. If it fails the pattern, or the password is empty or longer than 256 bytes, return `ErrInvalidCredentials`. No username limiter entry is created and nothing is hashed.
4. Take one token from the username's limiter (burst 5, `rate.Every(time.Minute)`), the same way.
5. Load the human. If there is none, or its hash is empty, run `verifyPassword` against a fixed dummy hash anyway (built once, on first use, with `hashPassword`) and return `ErrInvalidCredentials`, so a missing user costs the same time as a wrong password.
6. `verifyPassword`. False → `ErrInvalidCredentials`.
7. In one `InTx`: `DeleteExpiredSessions(now)`, then `PutSession` with a new ULID, `hashToken(token)`, and `expires_at = now + SessionLifetime`.

Both limiters are driven with the injected clock (`ReserveN(now, 1)`, `DelayFrom(now)`, `CancelAt(now)`), never the wall clock. One mutex guards the two maps and is held only around limiter calls, never across hashing or the database.

`ResolveSession` treats a session as expired when `now` is at or after `expires_at`.

- [ ] **Step 1: Write the failing tests** (package `bus_test`)

```go
// clockSvc returns a service whose clock the test can move, with kai already created.
func clockSvc(t *testing.T) (bus.Service, *time.Time, bus.Human) {
    now := fixed; s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return now })
    kai, err := s.CreateHuman(ctx, "kai", pw); require.NoError(t, err)
    return s, &now, kai
}

func TestLogin_Success(t *testing.T) {
    s, _, kai := clockSvc(t)
    sess, err := s.Login(ctx, " KAI ", pw, "10.0.0.1"); require.NoError(t, err)
    assert.Regexp(t, `^hcs_[A-Za-z0-9_-]{43}$`, sess.Token)
    assert.Equal(t, kai, sess.Human); assert.Equal(t, fixed.Add(bus.SessionLifetime), sess.ExpiresAt)
    actor, err := s.ResolveSession(ctx, sess.Token); require.NoError(t, err)
    assert.Equal(t, bus.Actor{Kind: bus.ActorHuman, ID: kai.ID}, actor)
    again, _ := s.Login(ctx, "kai", pw, "10.0.0.1"); assert.NotEqual(t, sess.Token, again.Token)
    _, err = s.ResolveSession(ctx, sess.Token); assert.NoError(t, err) // a second login does not end the first
}

func TestLogin_OnlyTheTokenHashIsStored(t *testing.T) {
    r := storagetest.NewRepo(t); s := bus.NewService(r, func() time.Time { return fixed })
    _, _ = s.CreateHuman(ctx, "kai", pw); sess, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
    _, err := r.GetSessionByTokenHash(ctx, sess.Token); assert.ErrorIs(t, err, storage.ErrNotFound)
    sum := sha256.Sum256([]byte(sess.Token))
    _, err = r.GetSessionByTokenHash(ctx, hex.EncodeToString(sum[:])); assert.NoError(t, err)
}

// Review Focus 3. Every failure is the same error, whatever the cause.
func TestLogin_InvalidCredentials(t *testing.T) {
    r := storagetest.NewRepo(t); s := bus.NewService(r, func() time.Time { return fixed })
    _, _ = s.CreateHuman(ctx, "kai", pw)
    require.NoError(t, r.PutHuman(ctx, storage.Human{ID: ulid.Make().String(), Username: "legacy"})) // stage 1 row: no password
    cases := [][2]string{{"kai", "wrong password"}, {"nobody", pw}, {"not a name", pw}, {"", pw}, {"kai", ""}, {"legacy", ""}, {"legacy", pw}, {"kai", strings.Repeat("a", 257)}}
    for i, c := range cases {
        _, err := s.Login(ctx, c[0], c[1], fmt.Sprintf("10.0.1.%d", i)) // a fresh IP each time
        assert.ErrorIs(t, err, bus.ErrInvalidCredentials, "%q", c)
        assert.Equal(t, bus.ErrInvalidCredentials.Error(), err.Error(), "%q: the error must not say which part was wrong", c)
    }
}

func TestLogin_ThrottlePerUsername(t *testing.T) {
    s, now, _ := clockSvc(t)
    for i := 0; i < 5; i++ { // burst of 5, each from a different IP
        _, err := s.Login(ctx, "kai", "wrong password", fmt.Sprintf("10.0.2.%d", i)); require.ErrorIs(t, err, bus.ErrInvalidCredentials)
    }
    _, err := s.Login(ctx, "kai", pw, "10.0.2.99") // even the right password
    var te bus.ThrottledError; require.ErrorAs(t, err, &te); assert.ErrorIs(t, err, bus.ErrThrottled)
    assert.InDelta(t, time.Minute, te.RetryAfter, float64(time.Millisecond))
    _, err = s.Login(ctx, "ada", "wrong password", "10.0.2.99"); assert.ErrorIs(t, err, bus.ErrInvalidCredentials) // other names are unaffected

    *now = now.Add(59 * time.Second)
    _, err = s.Login(ctx, "kai", pw, "10.0.2.99"); require.ErrorAs(t, err, &te)
    assert.InDelta(t, time.Second, te.RetryAfter, float64(time.Millisecond))
    *now = now.Add(2 * time.Second) // being refused did not push the wait further out
    _, err = s.Login(ctx, "kai", pw, "10.0.2.99"); assert.NoError(t, err)
}

func TestLogin_ThrottlePerIP(t *testing.T) {
    s, now, _ := clockSvc(t)
    for i := 0; i < 20; i++ { // burst of 20, a different name each time
        _, err := s.Login(ctx, fmt.Sprintf("user%d", i), "", "10.0.3.1"); require.ErrorIs(t, err, bus.ErrInvalidCredentials)
    }
    _, err := s.Login(ctx, "kai", pw, "10.0.3.1")
    var te bus.ThrottledError; require.ErrorAs(t, err, &te); assert.InDelta(t, 10*time.Second, te.RetryAfter, float64(time.Millisecond))
    _, err = s.Login(ctx, "kai", pw, "10.0.3.2"); assert.NoError(t, err) // another IP is fine, and kai's own bucket was not charged
    *now = now.Add(11 * time.Second)
    _, err = s.Login(ctx, "kai", pw, "10.0.3.1"); assert.NoError(t, err)
}

// Review Focus 2.
func TestLogin_ThrottleMapsDoNotGrowWithoutBound(t *testing.T) {
    now := fixed; s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return now })
    for i := 0; i < 3000; i++ { // empty password: refused at step 3, so no hashing
        _, _ = s.Login(ctx, fmt.Sprintf("user%d", i), "", fmt.Sprintf("10.1.%d.%d", i/250, i%250))
    }
    users, ips := s.ThrottleEntries()
    assert.Zero(t, users, "a name that never reaches the username limiter gets no entry")
    assert.Equal(t, 3000, ips) // the clock has not moved: none has refilled, so none may be dropped
    now = now.Add(time.Hour)
    _, _ = s.Login(ctx, "kai", "", "10.9.9.9")
    _, ips = s.ThrottleEntries(); assert.LessOrEqual(t, ips, 1)
}

// Review Focus 5.
func TestResolveSession_Expiry(t *testing.T) {
    s, now, _ := clockSvc(t); sess, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
    *now = fixed.Add(bus.SessionLifetime - time.Millisecond)
    _, err := s.ResolveSession(ctx, sess.Token); assert.NoError(t, err)
    *now = fixed.Add(bus.SessionLifetime)
    _, err = s.ResolveSession(ctx, sess.Token); assert.ErrorIs(t, err, bus.ErrUnauthenticated)
}

func TestResolveSession_BadTokens(t *testing.T) {
    s, _, _ := clockSvc(t); sess, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
    for _, bad := range []string{"", "hcs_", sess.Token + "x", strings.ToUpper(sess.Token)} {
        _, err := s.ResolveSession(ctx, bad); assert.ErrorIs(t, err, bus.ErrUnauthenticated, "%q", bad)
    }
    _, agentToken, _ := s.CreateAgent(ctx, "scout", "Scout")
    _, err := s.ResolveSession(ctx, agentToken); assert.ErrorIs(t, err, bus.ErrUnauthenticated) // an agent token is not a session
    _, err = s.ResolveAgentToken(ctx, sess.Token); assert.ErrorIs(t, err, bus.ErrUnauthenticated) // and the reverse
}

// Review Focus 5.
func TestSession_EndsOnLogoutPasswdAndRemove(t *testing.T) {
    s, _, _ := clockSvc(t)
    a, _ := s.Login(ctx, "kai", pw, "10.0.0.1"); b, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
    require.NoError(t, s.Logout(ctx, a.Token))
    _, err := s.ResolveSession(ctx, a.Token); assert.ErrorIs(t, err, bus.ErrUnauthenticated)
    _, err = s.ResolveSession(ctx, b.Token); assert.NoError(t, err) // logout ends one session, not all
    assert.NoError(t, s.Logout(ctx, a.Token)); assert.NoError(t, s.Logout(ctx, "")) // repeat and empty are fine

    require.NoError(t, s.SetHumanPassword(ctx, "kai", "a whole new password"))
    _, err = s.ResolveSession(ctx, b.Token); assert.ErrorIs(t, err, bus.ErrUnauthenticated)

    c, err := s.Login(ctx, "kai", "a whole new password", "10.0.0.2"); require.NoError(t, err)
    require.NoError(t, s.RemoveHuman(ctx, "kai"))
    _, err = s.ResolveSession(ctx, c.Token); assert.ErrorIs(t, err, bus.ErrUnauthenticated)
}

func TestLogin_SweepsExpiredSessions(t *testing.T) {
    now := fixed; r := storagetest.NewRepo(t); s := bus.NewService(r, func() time.Time { return now })
    _, _ = s.CreateHuman(ctx, "kai", pw); old, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
    now = fixed.Add(bus.SessionLifetime + time.Hour); _, err := s.Login(ctx, "kai", pw, "10.0.0.1"); require.NoError(t, err)
    sum := sha256.Sum256([]byte(old.Token))
    _, err = r.GetSessionByTokenHash(ctx, hex.EncodeToString(sum[:])); assert.ErrorIs(t, err, storage.ErrNotFound)
}

type sessionReadFails struct{ storage.Repository }
func (sessionReadFails) GetSessionByTokenHash(context.Context, string) (storage.Session, error) { return storage.Session{}, errDisk }

// A database failure is not "you are logged out".
func TestResolveSession_StorageFailureIsNotUnauthenticated(t *testing.T) {
    _, err := bus.NewService(sessionReadFails{storagetest.NewRepo(t)}, time.Now).ResolveSession(ctx, "hcs_x")
    assert.ErrorIs(t, err, errDisk); assert.NotErrorIs(t, err, bus.ErrUnauthenticated)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/bus/`
Expected: FAIL, `Login`, `ResolveSession`, `bus.ThrottledError` undefined.

- [ ] **Step 3: Implement `throttle.go` and `session.go`**

`go get golang.org/x/time`. `ThrottledError` mirrors `ValidationError`: a value type with an `Is` method.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./... && go vet ./...`
Expected: PASS, including `TestLayering_ThisRepository` (the bus still imports neither gin nor `net/http`).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/hermes-chat/internal/bus
git commit -m "Add sessions and throttled login to the bus"
```

---

### Task 5: App: error mapping, auth middleware, same-origin check

**Files:**
- Create: `cmd/hermes-chat/internal/app/errors.go`, `auth.go`, `export_test.go`
- Modify: `app/app.go` (`Controller`), `app/middleware.go` (`RequestLogger`)
- Test: `app/errors_test.go`, `app/auth_test.go`

**Interfaces:**
- Consumes: `bus.Service.ResolveSession`, `ResolveAgentToken`, `Login`, `CreateHuman`, `CreateAgent`, `RevokeAgent`; all bus sentinels; `bus.Actor`.
- Produces:
  ```go
  package app
  const SessionCookie = "hermes_session"
  const ActorKey = "actor" // gin context key; the value is a bus.Actor

  func SameOrigin() gin.HandlerFunc
  // added to Controller
  HumanAuth(c *gin.Context) // middleware
  AgentAuth(c *gin.Context) // middleware

  func writeError(c *gin.Context, err error) // unexported; every handler and middleware reports errors through it
  func actorFrom(c *gin.Context) bus.Actor   // unexported; for handlers behind an auth middleware

  // export_test.go
  var WriteError = writeError
  ```

Behavior pinned by this task:

- `writeError` writes `{"error":{"code":"<code>","message":"<message>"}}`, picking the first row that matches with `errors.Is`:

  | Bus error | Status | `code` | `message` |
  |---|---|---|---|
  | `ErrValidation` | 400 | `validation` | the `ValidationError`'s `Message` |
  | `ErrInvalidCredentials` | 401 | `invalid_credentials` | `wrong username or password` |
  | `ErrUnauthenticated` | 401 | `unauthenticated` | `authentication required` |
  | `ErrForbidden` | 403 | `forbidden` | `not allowed` |
  | `ErrNotFound` | 404 | `not_found` | `not found` |
  | `ErrConflict` | 409 | `conflict` | `already exists` |
  | `ErrThrottled` | 429 | `throttled` | `too many attempts; try again later` |
  | anything else | 500 | `internal` | `internal error` |

  A 429 also sets `Retry-After` to the wait in whole seconds, rounded up, at least 1. A 500 logs the real error at ERROR with `slog.ErrorContext`; nothing else is logged by `writeError`.
- `HumanAuth`: read the `hermes_session` cookie, call `ResolveSession`, on success `c.Set(ActorKey, actor)` and `c.Next()`. No cookie is `ErrUnauthenticated`. Any error goes to `writeError` and the chain is aborted.
- `AgentAuth`: the same with `Authorization: Bearer <token>` and `ResolveAgentToken`. The header must split with `strings.Fields` into exactly two fields, the first equal to `bearer` ignoring case. Every 401 from this middleware also sets `WWW-Authenticate: Bearer`.
- `SameOrigin`: `GET`, `HEAD`, and `OPTIONS` pass. Any other method needs an `Origin` header that parses as a URL whose `Host` equals `c.Request.Host` ignoring case; otherwise `writeError(c, bus.ErrForbidden)` and abort. *(This plan's call: comparing with the request's own `Host` needs no configuration and still stops another site's page, whose `Origin` is its own.)*
- `RequestLogger` adds `actor` (the `Actor`'s `String()`) when `ActorKey` is set, and omits the attribute otherwise.

- [ ] **Step 1: Write the failing tests** (package `app_test`)

```go
// errors_test.go
func TestWriteError_Table(t *testing.T) {
    cases := []struct{ err error; status int; body string }{
        {bus.ValidationError{Message: "username must be nicer"}, 400, `{"error":{"code":"validation","message":"username must be nicer"}}`},
        {fmt.Errorf("login: %w", bus.ErrInvalidCredentials), 401, `{"error":{"code":"invalid_credentials","message":"wrong username or password"}}`},
        {bus.ErrUnauthenticated, 401, `{"error":{"code":"unauthenticated","message":"authentication required"}}`},
        {bus.ErrForbidden, 403, `{"error":{"code":"forbidden","message":"not allowed"}}`},
        {fmt.Errorf("agent %q: %w", "secret-handle", bus.ErrNotFound), 404, `{"error":{"code":"not_found","message":"not found"}}`},
        {fmt.Errorf("username %q is already taken: %w", "kai", bus.ErrConflict), 409, `{"error":{"code":"conflict","message":"already exists"}}`},
        {bus.ThrottledError{RetryAfter: 1500 * time.Millisecond}, 429, `{"error":{"code":"throttled","message":"too many attempts; try again later"}}`},
        {errors.New("disk on fire"), 500, `{"error":{"code":"internal","message":"internal error"}}`},
        {fmt.Errorf("x: %w", storage.ErrNotFound), 500, `{"error":{"code":"internal","message":"internal error"}}`}, // a storage sentinel that leaked is a bug, not a 404
    }
    for _, tc := range cases {
        e, buf := newEngine(t, realSvc(t))
        e.GET("/fail", func(c *gin.Context) { app.WriteError(c, tc.err) })
        w := do(e, "GET", "/fail")
        assert.Equal(t, tc.status, w.Code, tc.err); assert.JSONEq(t, tc.body, w.Body.String(), tc.err)
        logged := strings.Contains(buf.String(), tc.err.Error())
        assert.Equal(t, tc.status == 500, logged, "only a 500 logs the real error: %v", tc.err)
        if tc.status == 429 { assert.Equal(t, "2", w.Header().Get("Retry-After")) } else { assert.Empty(t, w.Header().Get("Retry-After")) }
    }
}

func TestWriteError_RetryAfterIsAtLeastOne(t *testing.T) {
    e, _ := newEngine(t, realSvc(t))
    e.GET("/fail", func(c *gin.Context) { app.WriteError(c, bus.ThrottledError{RetryAfter: time.Millisecond}) })
    assert.Equal(t, "1", do(e, "GET", "/fail").Header().Get("Retry-After"))
}

// auth_test.go
const pw = "correct horse battery"

// authEngine is newEngine plus /h (HumanAuth) and /a (AgentAuth), each answering 200 with the actor's String().
// It returns a live session token for kai and a live bearer token for @scout.
func authEngine(t *testing.T) (e *gin.Engine, buf *bytes.Buffer, svc bus.Service, session, bearer string, kai bus.Human, scout bus.Agent)

func TestHumanAuth(t *testing.T) {
    e, buf, svc, session, bearer, kai, _ := authEngine(t)
    w := do(e, "GET", "/h", "Cookie", app.SessionCookie+"="+session)
    assert.Equal(t, 200, w.Code); assert.Equal(t, "human:"+kai.ID.String(), w.Body.String())
    assert.Equal(t, "human:"+kai.ID.String(), lines(t, buf)[0]["actor"]) // the request log names the actor
    assert.NotContains(t, buf.String(), session)

    for _, hdr := range [][]string{nil, {"Cookie", app.SessionCookie + "="}, {"Cookie", app.SessionCookie + "=nope"}, {"Cookie", "other=" + session}, {"Cookie", app.SessionCookie + "=" + bearer}, {"Authorization", "Bearer " + session}} {
        buf.Reset(); w := do(e, "GET", "/h", hdr...)
        assert.Equal(t, 401, w.Code, hdr); assert.JSONEq(t, `{"error":{"code":"unauthenticated","message":"authentication required"}}`, w.Body.String())
        assert.NotContains(t, lines(t, buf)[0], "actor")
    }
    require.NoError(t, svc.Logout(ctx, session))
    assert.Equal(t, 401, do(e, "GET", "/h", "Cookie", app.SessionCookie+"="+session).Code)
}

func TestAgentAuth(t *testing.T) {
    e, buf, svc, session, bearer, _, scout := authEngine(t)
    for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
        w := do(e, "GET", "/a", "Authorization", scheme+" "+bearer)
        assert.Equal(t, 200, w.Code, scheme); assert.Equal(t, "agent:"+scout.ID.String(), w.Body.String())
    }
    assert.NotContains(t, buf.String(), bearer)
    for _, v := range []string{"", bearer, "Bearer", "Bearer ", "Bearer " + bearer + " extra", "Basic " + bearer, "Bearer " + session} {
        var hdr []string; if v != "" { hdr = []string{"Authorization", v} }
        w := do(e, "GET", "/a", hdr...)
        assert.Equal(t, 401, w.Code, "%q", v); assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"), "%q", v)
    }
    assert.Equal(t, 401, do(e, "GET", "/a", "Cookie", app.SessionCookie+"="+session).Code) // a human cookie is not an agent credential
    require.NoError(t, svc.RevokeAgent(ctx, "scout"))
    assert.Equal(t, 401, do(e, "GET", "/a", "Authorization", "Bearer "+bearer).Code)
}

type resolveFails struct{ bus.Service }
func (resolveFails) ResolveSession(context.Context, string) (bus.Actor, error)    { return bus.Actor{}, errors.New("disk on fire") }
func (resolveFails) ResolveAgentToken(context.Context, string) (bus.Actor, error) { return bus.Actor{}, errors.New("disk on fire") }

func TestAuth_StorageFailureIs500AndNeverReachesTheHandler(t *testing.T) {
    e, _ := newEngine(t, nil); ctrl := app.NewController(resolveFails{realSvc(t)}); reached := false
    e.GET("/h", ctrl.HumanAuth, func(*gin.Context) { reached = true })
    e.GET("/a", ctrl.AgentAuth, func(*gin.Context) { reached = true })
    assert.Equal(t, 500, do(e, "GET", "/h", "Cookie", app.SessionCookie+"=x").Code)
    assert.Equal(t, 500, do(e, "GET", "/a", "Authorization", "Bearer x").Code)
    assert.False(t, reached)
}

// Review Focus 4. httptest.NewRequest sets Host to "example.com".
func TestSameOrigin(t *testing.T) {
    e, _ := newEngine(t, realSvc(t)); hits := 0
    e.Any("/x", app.SameOrigin(), func(c *gin.Context) { hits++; c.Status(204) })
    for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
        assert.Equal(t, 204, do(e, m, "/x").Code, m)                                  // no Origin needed to read
        assert.Equal(t, 204, do(e, m, "/x", "Origin", "https://evil.test").Code, m)
    }
    for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
        for _, ok := range []string{"https://example.com", "http://example.com", "https://EXAMPLE.com"} {
            assert.Equal(t, 204, do(e, m, "/x", "Origin", ok).Code, "%s %s", m, ok)
        }
        before := hits
        for _, bad := range []string{"", "null", "https://evil.test", "https://example.com.evil.test", "https://example.com:8443", "https://evil.test/example.com", "example.com", "://"} {
            var hdr []string; if bad != "" { hdr = []string{"Origin", bad} }
            w := do(e, m, "/x", hdr...)
            assert.Equal(t, 403, w.Code, "%s %q", m, bad)
            assert.JSONEq(t, `{"error":{"code":"forbidden","message":"not allowed"}}`, w.Body.String())
        }
        assert.Equal(t, before, hits, "a refused request never reaches the handler")
    }
}
```

`newEngine` in `app_test.go` keeps working as it is. Where a test passes `nil` for the service it registers its own routes.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/app/`
Expected: FAIL, `app.WriteError`, `app.SameOrigin`, `HumanAuth` undefined.

- [ ] **Step 3: Implement `errors.go`, the middleware in `auth.go`, the `Controller` additions, and the `actor` attribute in `RequestLogger`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/app
git commit -m "Add error mapping, auth middleware, and the same-origin check"
```

---

### Task 6: Login, logout, and `me`: handlers, routes, config, `serve`

**Files:**
- Modify: `cmd/hermes-chat/internal/app/app.go`, `app/auth.go`, `cmd/hermes-chat/config.go`, `wires.go`, `routes.go`, `serve.go`, `README.md`
- Test: `app/auth_test.go`, `app/app_test.go`, `cmd/hermes-chat/routes_test.go`, `cmd/hermes-chat/config_test.go`, `serve_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  ```go
  package app
  type Options struct {
      CookieSecure bool // the Secure attribute on the session cookie
  }
  func NewController(svc bus.Service, opts Options) *controller
  // added to Controller
  Login(c *gin.Context)
  Logout(c *gin.Context)
  Me(c *gin.Context)
  AgentMe(c *gin.Context)

  package main
  // added to Config
  CookieSecure   bool     `split_words:"true" default:"true"` // HERMES_CHAT_COOKIE_SECURE
  TrustedProxies []string `split_words:"true"`                // HERMES_CHAT_TRUSTED_PROXIES, comma-separated IPs or CIDRs; empty trusts none
  // test helper
  func startServeWithEnv(t *testing.T) (api, debug string, cancel func(), done <-chan error) // startServe without the setDB call; startServe becomes setDB + this
  ```

Behavior pinned by this task:

- Routes, all registered in `routes.go` and nowhere else:

  | Group | Middleware | Routes |
  |---|---|---|
  | public | | `GET /healthz`; `POST /api/login` (with `app.SameOrigin()`) |
  | human API, `/api` | `app.SameOrigin()`, `ctrl.HumanAuth` | `POST /api/logout`, `GET /api/me` |
  | agent API, `/agent` | `ctrl.AgentAuth` | `GET /agent/me` |

- `Login`: body `{"username":"…","password":"…"}`, read through `http.MaxBytesReader` at 4096 bytes; unknown fields are ignored. A body that is too large, not JSON, or not an object is 400 `{"error":{"code":"bad_request","message":"request body must be a JSON object with username and password"}}`. Then `svc.Login(ctx, username, password, c.ClientIP())`; errors go to `writeError`. Success is 200 `{"id":"<ULID>","username":"kai"}` with the cookie `hermes_session=<token>; Path=/; Max-Age=7776000; HttpOnly; SameSite=Lax`, plus `Secure` when `Options.CookieSecure`.
- `Logout`: `svc.Logout` with the cookie's token, then 204 and the same cookie with an empty value and `Max-Age=0`.
- `Me`: 200 `{"id":"<ULID>","username":"kai"}` from `svc.GetHuman(actor.ID)`. `AgentMe`: 200 `{"id":"<ULID>","handle":"scout","display_name":"Scout"}` from `svc.GetAgent(actor.ID)`.
- `serve` calls `e.SetTrustedProxies(cfg.TrustedProxies)` right after `gin.New()`, so with the default empty list `c.ClientIP()` is the TCP peer and `X-Forwarded-For` is ignored.
- `validateForServe` rejects a `TrustedProxies` entry that is neither an IP nor a CIDR, naming `HERMES_CHAT_TRUSTED_PROXIES`.

- [ ] **Step 1: Write the failing tests**

```go
// app/auth_test.go (package app_test). apiEngine builds the real route table by hand, as routes.go will:
// public login, the human group, the agent group. secure sets Options.CookieSecure.
func apiEngine(t *testing.T, secure bool) (e *gin.Engine, buf *bytes.Buffer, svc bus.Service)
// post sends a JSON body with Origin: https://example.com and any extra headers.
func post(e *gin.Engine, target, body string, hdr ...string) *httptest.ResponseRecorder
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie // the hermes_session cookie from Set-Cookie

func TestLogin_SetsTheSessionCookie(t *testing.T) {
    e, buf, svc := apiEngine(t, true); kai, _ := svc.CreateHuman(ctx, "kai", pw)
    w := post(e, "/api/login", `{"username":" Kai ","password":"`+pw+`","extra":1}`)
    require.Equal(t, 200, w.Code); assert.JSONEq(t, `{"id":"`+kai.ID.String()+`","username":"kai"}`, w.Body.String())
    c := sessionCookie(t, w)
    assert.Regexp(t, `^hcs_[A-Za-z0-9_-]{43}$`, c.Value); assert.Equal(t, "/", c.Path); assert.Equal(t, 7776000, c.MaxAge)
    assert.True(t, c.HttpOnly); assert.True(t, c.Secure); assert.Equal(t, http.SameSiteLaxMode, c.SameSite); assert.Empty(t, c.Domain)
    assert.NotContains(t, w.Body.String(), c.Value)
    for _, secret := range []string{c.Value, pw} { assert.NotContains(t, buf.String(), secret) }

    me := do(e, "GET", "/api/me", "Cookie", app.SessionCookie+"="+c.Value)
    assert.Equal(t, 200, me.Code); assert.JSONEq(t, `{"id":"`+kai.ID.String()+`","username":"kai"}`, me.Body.String())
}

func TestLogin_CookieNotSecureWhenConfiguredOff(t *testing.T) {
    e, _, svc := apiEngine(t, false); _, _ = svc.CreateHuman(ctx, "kai", pw)
    assert.False(t, sessionCookie(t, post(e, "/api/login", `{"username":"kai","password":"`+pw+`"}`)).Secure)
}

// An unknown user and a wrong password must be indistinguishable.
func TestLogin_FailuresLookTheSame(t *testing.T) {
    e, _, svc := apiEngine(t, true); _, _ = svc.CreateHuman(ctx, "kai", pw)
    a := post(e, "/api/login", `{"username":"kai","password":"wrong password"}`)
    b := post(e, "/api/login", `{"username":"nobody","password":"wrong password"}`)
    assert.Equal(t, 401, a.Code); assert.Equal(t, a.Code, b.Code); assert.Equal(t, a.Body.String(), b.Body.String())
    assert.JSONEq(t, `{"error":{"code":"invalid_credentials","message":"wrong username or password"}}`, a.Body.String())
    assert.Empty(t, a.Header().Values("Set-Cookie")); assert.Empty(t, b.Header().Values("Set-Cookie"))
}

func TestLogin_BadBodies(t *testing.T) {
    e, _, _ := apiEngine(t, true)
    for _, body := range []string{"", "not json", `[]`, `"kai"`, `{"username":1}`, `{"username":"kai","password":"` + strings.Repeat("a", 5000) + `"}`} {
        w := post(e, "/api/login", body)
        assert.Equal(t, 400, w.Code, "%.20q", body)
        assert.JSONEq(t, `{"error":{"code":"bad_request","message":"request body must be a JSON object with username and password"}}`, w.Body.String())
    }
    assert.Equal(t, 401, post(e, "/api/login", `{}`).Code) // well-formed but empty is just wrong credentials
}

func TestLogin_NeedsSameOriginAndThrottles(t *testing.T) {
    e, _, svc := apiEngine(t, true); _, _ = svc.CreateHuman(ctx, "kai", pw)
    good := `{"username":"kai","password":"` + pw + `"}`
    assert.Equal(t, 403, post(e, "/api/login", good, "Origin", "https://evil.test").Code) // login CSRF
    for i := 0; i < 5; i++ { require.Equal(t, 401, post(e, "/api/login", `{"username":"kai","password":"wrong password"}`).Code) }
    w := post(e, "/api/login", good)
    assert.Equal(t, 429, w.Code); assert.Regexp(t, `^[1-9][0-9]?$`, w.Header().Get("Retry-After"))
    assert.Empty(t, w.Header().Values("Set-Cookie"))
}

func TestLogout(t *testing.T) {
    e, _, svc := apiEngine(t, true); _, _ = svc.CreateHuman(ctx, "kai", pw)
    tok := sessionCookie(t, post(e, "/api/login", `{"username":"kai","password":"`+pw+`"}`)).Value
    cookie := app.SessionCookie + "=" + tok
    assert.Equal(t, 403, post(e, "/api/logout", "", "Cookie", cookie, "Origin", "https://evil.test").Code) // another site cannot log you out
    assert.Equal(t, 200, do(e, "GET", "/api/me", "Cookie", cookie).Code)
    w := post(e, "/api/logout", "", "Cookie", cookie)
    assert.Equal(t, 204, w.Code)
    gone := sessionCookie(t, w); assert.Empty(t, gone.Value); assert.Less(t, gone.MaxAge, 0) // net/http reports Max-Age=0 as -1
    assert.Equal(t, 401, do(e, "GET", "/api/me", "Cookie", cookie).Code)
    assert.Equal(t, 401, post(e, "/api/logout", "", "Cookie", cookie).Code)
    assert.Equal(t, 401, post(e, "/api/logout", "").Code)
}

func TestAgentMe(t *testing.T) {
    e, _, svc := apiEngine(t, true); a, token, _ := svc.CreateAgent(ctx, "scout", "Scout")
    w := do(e, "GET", "/agent/me", "Authorization", "Bearer "+token)
    assert.Equal(t, 200, w.Code); assert.JSONEq(t, `{"id":"`+a.ID.String()+`","handle":"scout","display_name":"Scout"}`, w.Body.String())
    assert.Equal(t, 401, do(e, "GET", "/agent/me").Code)
    assert.Equal(t, 401, do(e, "GET", "/api/me", "Authorization", "Bearer "+token).Code) // the groups do not accept each other's credentials
}

// cmd/hermes-chat/config_test.go: add CookieSecure: true to the expected value in TestLoadConfig_Defaults. Then add:
func TestLoadConfig_AuthSettings(t *testing.T) {
    setDB(t); t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false"); t.Setenv("HERMES_CHAT_TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8")
    c, err := loadConfig(); require.NoError(t, err)
    assert.False(t, c.CookieSecure); assert.Equal(t, []string{"127.0.0.1", "10.0.0.0/8"}, c.TrustedProxies)
    assert.NoError(t, c.validateForServe())
    c.TrustedProxies = []string{"not-an-ip"}
    err = c.validateForServe(); require.Error(t, err); assert.Contains(t, err.Error(), "HERMES_CHAT_TRUSTED_PROXIES")
}

// cmd/hermes-chat/serve_test.go
// login posts to /api/login with Origin set to the API's own base URL and returns the response.
func login(t *testing.T, client *http.Client, api, username, password string, hdr ...string) *http.Response
func status(t *testing.T, client *http.Client, req *http.Request) int

// The whole stage, across two "processes": the CLI and a running server sharing one database file.
func TestServe_AuthEndToEnd(t *testing.T) { // Review Focus 5
    migrated(t); t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false") // the test speaks plain HTTP
    runCmdIn(t, pw+"\n", "user", "add", "kai")
    _, out, _ := runCmd(t, "agent", "add", "scout", "Scout"); bearer := tokenLine.FindStringSubmatch(out)[1]
    api, _, _, _ := startServeWithEnv(t)
    jar, _ := cookiejar.New(nil); client := &http.Client{Jar: jar}
    me, _ := http.NewRequest("GET", api+"/api/me", nil)
    agentMe, _ := http.NewRequest("GET", api+"/agent/me", nil); agentMe.Header.Set("Authorization", "Bearer "+bearer)

    assert.Equal(t, 401, status(t, client, me))
    resp := login(t, client, api, "kai", pw); require.Equal(t, 200, resp.StatusCode)
    assert.Equal(t, 200, status(t, client, me)) // the jar carried the cookie
    assert.Equal(t, 200, status(t, http.DefaultClient, agentMe))

    code, _, _ := runCmdIn(t, "a whole new password\n", "user", "passwd", "kai"); require.Equal(t, 0, code) // while the server is running
    assert.Equal(t, 401, status(t, client, me))
    code, _, _ = runCmd(t, "agent", "revoke", "scout"); require.Equal(t, 0, code)
    assert.Equal(t, 401, status(t, http.DefaultClient, agentMe))
}

// Review Focus 1. An empty password is refused before any hashing, but still costs the IP a token.
func TestServe_ForwardedForCannotDodgeTheIPThrottle(t *testing.T) {
    api, _, _, _ := startServe(t)
    for i := 0; i < 20; i++ {
        resp := login(t, http.DefaultClient, api, fmt.Sprintf("user%d", i), "", "X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
        require.Equal(t, 401, resp.StatusCode, i)
    }
    resp := login(t, http.DefaultClient, api, "user20", "", "X-Forwarded-For", "203.0.113.20")
    assert.Equal(t, 429, resp.StatusCode)
    n, err := strconv.Atoi(resp.Header.Get("Retry-After")); require.NoError(t, err); assert.True(t, n >= 1 && n <= 10, n)
}

func TestServe_TrustedProxyForwardedForIsHonored(t *testing.T) {
    t.Setenv("HERMES_CHAT_TRUSTED_PROXIES", "127.0.0.1")
    api, _, _, _ := startServe(t)
    for i := 0; i < 25; i++ { // more than one IP's burst, but each from a "different client"
        resp := login(t, http.DefaultClient, api, fmt.Sprintf("user%d", i), "", "X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
        require.Equal(t, 401, resp.StatusCode, i)
    }
}
```

Also give every existing `app.NewController(…)` call in the tests (`app_test.go`, `auth_test.go`, `routes_test.go`) a second argument, `app.Options{}`, and extend `TestRoutes_MiddlewareChain` with: `POST /api/logout` with a same-origin `Origin` and no cookie is 401; `GET /agent/me` with no header is 401.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile: `app.Options`, `Login`, `startServeWithEnv` undefined.

- [ ] **Step 3: Implement** the four handlers, `Options`, the `Config` fields and their validation, `wires.go` (pass `app.Options{CookieSecure: cfg.CookieSecure}`), `routes.go`, and `SetTrustedProxies` in `serve.go`.

- [ ] **Step 4: Run to verify pass**

Run: `just check`
Expected: PASS; `sqlc diff` clean.

- [ ] **Step 5: Update `README.md`**

Add the two variables to the configuration table (`HERMES_CHAT_COOKIE_SECURE`, default `true`, set `false` only for plain HTTP on a development machine; `HERMES_CHAT_TRUSTED_PROXIES`, default empty, needed only behind a reverse proxy). Replace the commands block with all ten commands, saying that `user add` and `user passwd` prompt for the password on a terminal and otherwise read one line from standard input, and that `agent add` and `agent rotate` print the token once. Remove the paragraph saying `user add` creates a human without a password, and add in its place that a human created before passwords existed needs `user passwd` before logging in. Add a short "HTTP API" section listing the five routes, the error body shape, and the status table from Task 5.

- [ ] **Step 6: Run it by hand**

```bash
export HERMES_CHAT_DB_PATH=/tmp/hermes-stage2.db HERMES_CHAT_COOKIE_SECURE=false
rm -f /tmp/hermes-stage2.db*
go run ./cmd/hermes-chat migrate
echo 'correct horse battery' | go run ./cmd/hermes-chat user add kai
go run ./cmd/hermes-chat agent add scout 'Scout'          # copy the token
go run ./cmd/hermes-chat serve &
sleep 1
O='Origin: http://localhost:8080'
curl -si -c /tmp/jar -H "$O" -d '{"username":"kai","password":"correct horse battery"}' localhost:8080/api/login
curl -s  -b /tmp/jar localhost:8080/api/me
curl -s  -o /dev/null -w '%{http_code}\n' -d '{}' localhost:8080/api/login
curl -s  -H 'Authorization: Bearer <token>' localhost:8080/agent/me
curl -s  -o /dev/null -w '%{http_code}\n' -b /tmp/jar -H "$O" -X POST localhost:8080/api/logout
curl -s  -o /dev/null -w '%{http_code}\n' -b /tmp/jar localhost:8080/api/me
kill -INT %1; wait %1
```

Expected, in order: `200` with a `Set-Cookie: hermes_session=hcs_…; Path=/; Max-Age=7776000; HttpOnly; SameSite=Lax` header; `{"id":"…","username":"kai"}`; `403` (no `Origin`); `{"id":"…","handle":"scout","display_name":"Scout"}`; `204`; `401`. The JSON log on stderr has one `request` line per call, the `/api/me` and `/agent/me` lines carry `"actor":"human:…"` and `"actor":"agent:…"`, and no line contains the password, the cookie value, or the bearer token.

- [ ] **Step 7: Cross-compile and commit**

```bash
just build linux amd64 && just build linux arm64
git add README.md go.mod go.sum cmd/hermes-chat
git commit -m "Add login, logout, and me endpoints with trusted proxy config"
```
