# Stage 7: TLS and Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The binary serves HTTPS itself with a Let's Encrypt certificate and HTTP/2, and the repository carries everything an operator needs to run it on a Debian host: a systemd unit, an example environment file, an on-box upgrade script with rollback, and a runbook.

**Architecture:** `serve` gains a TLS mode. In `autocert` mode it opens the API listener itself, wraps it with `autocert.Manager.TLSConfig()`, and hands it to grace's `httpserver` through `WithListener`; `off` keeps today's plain HTTP path. Deployment is manual by design: build, `scp` the binary, run one script on the box. Nothing in the repository names a particular instance.

**Tech Stack:** Go 1.26, `golang.org/x/crypto/acme/autocert` (already a direct dependency at v0.58.0), `github.com/quells/grace` v0.1.0, systemd on Debian 13, bash, shellcheck, ufw.

**Spec:** `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (sections 5.4, 6.4, 11, 12 "To verify", 14) and `docs/superpowers/specs/2026-10-11-hermes-chat-design.md` (section 9). Deploy decisions agreed with Kai on 2026-10-11 are listed under Global Constraints and written into the spec in Task 4.

## Global Constraints

- TLS uses `golang.org/x/crypto/acme/autocert`; the TLS-ALPN challenge is answered on the API port, so only 443 needs to be open. No port-80 listener, no redirect. (spec 6.4)
- TLS mode `off` serves plain HTTP on the same runner. (spec 6.4)
- Config is `HERMES_CHAT_`-prefixed envconfig fields; settings only `serve` needs are validated in `validateForServe`, not at load. (spec 5.4)
- No cgo dependency; the binary is built with `CGO_ENABLED=0`. (spec 11)
- Scripts start with `#!/usr/bin/env bash` and `set -euo pipefail`, pass `shellcheck`, and run on bash 3.2 with BSD userland: no associative arrays, `mapfile`, `${var,,}`, `&>>`, `wait -n`, and no GNU-only flags (`head -n -5`, `sed -i` without a suffix, `date -d`, `readlink -f`). This applies to the on-box scripts too, because their tests run wherever `go test ./...` runs. (spec 11)
- **No deploy command** *(Kai)*. There is no `just deploy`; the operator builds, copies the binary with `scp`, and runs the upgrade script on the box.
- **The repository is public and names no instance** *(Kai)*. Committed files use `chat.example.com` and `203.0.113.10`. Kai's host address and domain are given to the worker by the controller for Task 5 and are never written to a tracked file, commit message, or this plan.
- Layout on the host *(Claude's call, accepted by Kai)*: binary `/usr/local/bin/hermes-chat`, previous binary `/usr/local/bin/hermes-chat.previous`, environment file `/etc/hermes-chat/env`, state directory `/var/lib/hermes-chat` (database `chat.db`, `attachments/`, `certs/`, `backups/`), system user and group `hermes-chat`, unit `hermes-chat.service`, scripts `/usr/local/sbin/hermes-chat-upgrade` and `/usr/local/sbin/hermes-chat-admin`.
- `serve` already applies migrations at start (`wireUp`), so an upgrade is: stop, copy the database aside, swap the binary, start, check `/healthz`.
- Run `just check` before every commit.

## Review Focus

1. **A published AAAA record whose address does not reach the box.** Let's Encrypt prefers IPv6, so issuance fails although IPv4 works. Expected: the runbook has the operator test IPv6 reachability before the first start. Pinned in Task 4 (runbook) and Task 5 step 5.
2. **`HERMES_CHAT_TLS_DOMAIN` typed as a URL, with a port, in upper case, or as an IP address.** Expected: `serve` refuses to start and names the variable. Pinned by `TestValidateForServe_TLS` in Task 1.
3. **An upgrade candidate that cannot run on the host** (wrong architecture, truncated upload, a text file). Expected: the script refuses before stopping the service. Pinned by `TestUpgrade_RejectsCandidateThatDoesNotRun` in Task 3.
4. **A new version that migrates the database and then fails its health check.** Expected: the old binary and the pre-upgrade database are both restored and the service is running again. Pinned by `TestUpgrade_RollsBackBinaryAndDatabase` in Task 3.
5. **Repeated upgrades filling the disk with database copies.** Expected: only the newest five are kept. Pinned by `TestUpgrade_KeepsFiveBackups` in Task 3.

---

### Task 1: TLS configuration

**Files:**
- Modify: `cmd/hermes-chat/config.go`
- Modify: `cmd/hermes-chat/config_test.go`
- Modify: `cmd/hermes-chat/main_test.go:20-25` (`setDB`)
- Modify: `README.md` (configuration table, the paragraph on running locally)

**Interfaces:**
- Produces: `Config.TLSMode string` (`"autocert"` or `"off"`), `Config.TLSDomain string`, `Config.TLSCacheDir string`, `func (c Config) tlsCacheDir() string`. `validateForServe` rejects bad TLS settings.

Decisions:

| Variable | Default | Rule |
|---|---|---|
| `HERMES_CHAT_TLS_MODE` | `autocert` | `autocert` or `off`; anything else is an error from `validateForServe`. |
| `HERMES_CHAT_TLS_DOMAIN` | none | Required when the mode is `autocert`, ignored when `off`. One lower-case DNS name containing at least one dot: no scheme, no `/`, no `:`, no whitespace, no `*`, not an IP address. |
| `HERMES_CHAT_TLS_CACHE_DIR` | `certs` beside the database file | Same shape as `attachmentsDir()`. |

The default is `autocert` so that a production host that forgets the setting fails loudly instead of serving plain HTTP *(Claude's call)*. `HERMES_CHAT_HTTP_ADDR` keeps its `:8080` default; a production environment file sets `:443`.

- [ ] **Step 1: Write the failing tests** in `config_test.go`

```go
func TestLoadConfig_TLSDefaults(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/var/lib/hermes-chat/chat.db")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "autocert", c.TLSMode)
	assert.Equal(t, "", c.TLSDomain)
	assert.Equal(t, "/var/lib/hermes-chat/certs", c.tlsCacheDir())
	t.Setenv("HERMES_CHAT_TLS_CACHE_DIR", "/srv/certs")
	c, err = loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "/srv/certs", c.tlsCacheDir())
}

func TestValidateForServe_TLS(t *testing.T) {
	for _, tc := range []struct{ mode, domain, wantErr string }{
		{"autocert", "chat.example.com", ""},
		{"off", "", ""},
		{"off", "not a domain", ""}, // ignored when off
		{"on", "chat.example.com", "HERMES_CHAT_TLS_MODE"},
		{"autocert", "", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "https://chat.example.com", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "chat.example.com:443", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "chat.example.com/", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "Chat.Example.com", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "*.example.com", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "203.0.113.10", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "localhost", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", " chat.example.com", "HERMES_CHAT_TLS_DOMAIN"},
	} {
		t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
		t.Setenv("HERMES_CHAT_TLS_MODE", tc.mode)
		t.Setenv("HERMES_CHAT_TLS_DOMAIN", tc.domain)
		c, err := loadConfig()
		require.NoError(t, err)
		err = c.validateForServe()
		if tc.wantErr == "" {
			assert.NoError(t, err, "%s %q", tc.mode, tc.domain)
			continue
		}
		require.Error(t, err, "%s %q", tc.mode, tc.domain)
		assert.Contains(t, err.Error(), tc.wantErr)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./cmd/hermes-chat -run 'TLS' -v`
Expected: build failure, `c.TLSMode undefined`.

- [ ] **Step 3: Add the three fields, `tlsCacheDir()`, and the checks in `validateForServe`** in `config.go`. Field comments follow the existing ones (the variable name, then what it means).

- [ ] **Step 4: Keep the existing tests on plain HTTP.** Add `t.Setenv("HERMES_CHAT_TLS_MODE", "off")` to `setDB` in `main_test.go`, and to each existing test in `config_test.go` that expects `validateForServe` to succeed without going through `setDB`.

- [ ] **Step 5: Run the whole package**

Run: `go test ./cmd/hermes-chat`
Expected: `ok`.

- [ ] **Step 6: Document.** Add the three rows to the README configuration table. Wherever the README shows `serve` being run locally, say that a development machine sets `HERMES_CHAT_TLS_MODE=off` and `HERMES_CHAT_COOKIE_SECURE=false`.

- [ ] **Step 7: Commit**

```bash
just check
git add cmd/hermes-chat/config.go cmd/hermes-chat/config_test.go cmd/hermes-chat/main_test.go README.md
git commit -m "feat(config): TLS mode, domain, and certificate cache directory"
```

---

### Task 2: Serve HTTPS with autocert, and check HTTP/2

**Files:**
- Create: `cmd/hermes-chat/tls.go`
- Create: `cmd/hermes-chat/tls_test.go`
- Modify: `cmd/hermes-chat/serve.go`
- Modify: `README.md:488-489` (the known limit about six streams), `go.mod`/`go.sum` if `go mod tidy` changes them
- Modify: `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (section 12, the "Not checked" bullet)

**Interfaces:**
- Consumes: `Config.TLSMode`, `Config.TLSDomain`, `Config.tlsCacheDir()` from Task 1.
- Produces:
  - `func certManager(domain, cacheDir string) *autocert.Manager` with `Prompt: autocert.AcceptTOS`, `HostPolicy: autocert.HostWhitelist(domain)`, `Cache: autocert.DirCache(cacheDir)`.
  - `func apiListener(cfg Config, supplied net.Listener) (net.Listener, error)`. Mode `off`: returns `supplied` unchanged, which may be nil. Mode `autocert`: uses `supplied`, or `net.Listen("tcp", cfg.HTTPAddr)` when it is nil, and returns `tls.NewListener(ln, certManager(...).TLSConfig())`.

How the test avoids a real certificate authority: autocert serves a certificate found in its cache without contacting ACME, as long as it matches the host, has not expired, and matches its key. The cache entry is a file named after the domain holding an `EC PRIVATE KEY` PEM block followed by the `CERTIFICATE` block. A throwaway run on 2026-10-11 confirmed that a self-signed P-256 certificate placed there is served, and that HTTP/2 is negotiated through `tls.NewListener` → `httpserver.WithListener` → `http.Server.Serve` on grace v0.1.0. This task turns that into a permanent test.

- [ ] **Step 1: Write the failing tests** in `tls_test.go`

Helpers, both in `tls_test.go`:
- `writeTestCert(t *testing.T, dir, domain string) *x509.Certificate` generates a self-signed P-256 certificate for `domain`, valid from an hour ago for 90 days, writes key and certificate to `filepath.Join(dir, domain)` with mode `0600`, and returns the parsed certificate.
- `startTLSServe(t *testing.T, domain string) (addr string, client *http.Client)` sets `HERMES_CHAT_TLS_MODE=autocert`, `HERMES_CHAT_TLS_DOMAIN=domain`, `HERMES_CHAT_TLS_CACHE_DIR` to a temp directory holding `writeTestCert`'s output, calls `setDB` first, then runs `serve` on two `127.0.0.1:0` listeners the way `startServeWithEnv` does. The client trusts only that certificate, sets `ForceAttemptHTTP2: true`, and dials `addr` whatever host the URL names.

```go
func TestServe_AutocertNegotiatesHTTP2(t *testing.T) {
	_, client := startTLSServe(t, "chat.test")
	resp, err := client.Get("https://chat.test/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, resp.ProtoMajor, "got %s", resp.Proto)
}

func TestServe_AutocertRefusesOtherHosts(t *testing.T) {
	addr, _ := startTLSServe(t, "chat.test")
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "other.test", InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
	}
	require.Error(t, err) // the handshake fails; no certificate is requested for a host not configured
}

func TestServe_AutocertAnswersPlainHTTPWith400(t *testing.T) {
	addr, _ := startTLSServe(t, "chat.test")
	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAPIListener_OffReturnsSuppliedUnchanged(t *testing.T) {
	ln, err := apiListener(Config{TLSMode: "off"}, nil)
	require.NoError(t, err)
	assert.Nil(t, ln)
}

func TestAPIListener_AutocertReportsAddressInUse(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer taken.Close()
	_, err = apiListener(Config{TLSMode: "autocert", TLSDomain: "chat.test", DBPath: filepath.Join(t.TempDir(), "x.db"), HTTPAddr: taken.Addr().String()}, nil)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./cmd/hermes-chat -run 'Autocert|APIListener' -v`
Expected: build failure, `undefined: apiListener`.

- [ ] **Step 3: Write `certManager` and `apiListener`** in `tls.go`.

- [ ] **Step 4: Call it from `serve`.** Replace `ln.api` with the result of `apiListener(cfg, ln.api)` before building `apiOpts`; return its error. A listener `serve` opened itself is closed by `http.Server.Shutdown`, so nothing is added to the clean-up list.

- [ ] **Step 5: Run the package**

Run: `go test ./cmd/hermes-chat`
Expected: `ok`, including the three `Autocert` tests.

- [ ] **Step 6: Record the result.** In the architecture spec section 12, change the "Not checked" bullet to say it was confirmed in stage 7 and name `TestServe_AutocertNegotiatesHTTP2`; update the sentence above the list that says the fourth is still open. In `README.md` known limits, replace "Stage 7 checks HTTP/2." with a sentence saying that `autocert` mode negotiates HTTP/2, so the limit applies only to TLS mode `off` without an HTTP/2 proxy in front.

- [ ] **Step 7: Commit**

```bash
go mod tidy
just check
git add cmd/hermes-chat/tls.go cmd/hermes-chat/tls_test.go cmd/hermes-chat/serve.go README.md go.mod go.sum docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md
git commit -m "feat(serve): HTTPS with autocert on the API listener, HTTP/2 confirmed"
```

---

### Task 3: The on-box upgrade script

**Files:**
- Create: `deploy/hermes-chat-upgrade` (mode `0755`)
- Create: `deploy/upgrade_test.go` (package `deploy`)
- Modify: `justfile`, `README.md` (recipes list)

**Interfaces:**
- Produces: `hermes-chat-upgrade CANDIDATE`. Exit 0 when the new version is running and healthy; non-zero otherwise, with one line on stderr saying what happened. Task 4's runbook and Task 5 call it by this name.

Overrides, read from the script's own environment, used by the tests and by an operator with a different layout:

| Variable | Default |
|---|---|
| `HERMES_CHAT_ENV_FILE` | `/etc/hermes-chat/env` |
| `HERMES_CHAT_BIN` | `/usr/local/bin/hermes-chat` |
| `HERMES_CHAT_UNIT` | `hermes-chat` |
| `HERMES_CHAT_HEALTH_TIMEOUT` | `60` (seconds) |

What it does, in order:

1. Requires exactly one argument; otherwise prints `usage: hermes-chat-upgrade CANDIDATE` and exits 2.
2. Runs the candidate with no arguments. It must exit 2 and print a first line of `usage: hermes-chat <command>` on stderr. Anything else: exit 1, `candidate does not run on this host`. Nothing has been touched yet.
3. Reads settings from the environment file **without sourcing it**: the last line matching `^NAME=` wins, the value is the rest of the line. It needs `HERMES_CHAT_DB_PATH` (must be absolute), and for the health check `HERMES_CHAT_TLS_MODE` (default `autocert`), `HERMES_CHAT_TLS_DOMAIN`, and `HERMES_CHAT_HTTP_ADDR` (default `:8080`; only the port after the last `:` is used). A missing file or database path: exit 1 before stopping anything.
4. `systemctl stop "$HERMES_CHAT_UNIT"`.
5. If the database file exists, copies it and any `-wal` and `-shm` beside it, with `cp -p`, into `<database directory>/backups/<UTC stamp>/`, stamp from `date -u +%Y%m%dT%H%M%SZ`. Then removes all but the five newest stamp directories.
6. Copies the candidate to `$HERMES_CHAT_BIN.new` with mode `0755`. If a current binary exists, moves it to `$HERMES_CHAT_BIN.previous`. Moves `.new` into place.
7. `systemctl start "$HERMES_CHAT_UNIT"`, then polls the health URL once a second until it answers or the timeout passes. The call is `curl -fsS -o /dev/null --max-time 5 URL`, with `--resolve DOMAIN:PORT:127.0.0.1` before the URL in `autocert` mode. URL: `https://DOMAIN:PORT/healthz` in `autocert` mode, `http://127.0.0.1:PORT/healthz` when `off`.
8. Healthy: prints `upgraded; previous binary kept at <path>` (or `installed` on a first install) and exits 0.
9. Not healthy: stops the unit. If there was a previous binary, moves it back, restores the database files from this run's backup (removing a `-wal` or `-shm` the backup does not have), starts the unit, prints `new version failed its health check; rolled back to the previous version` on stderr, and exits 1. If there was none, leaves the unit stopped, prints `new version failed its health check; nothing to roll back to`, and exits 1.

- [ ] **Step 1: Write the failing tests** in `deploy/upgrade_test.go`

The harness, `newBox(t) *box`, builds a temp directory with:
- `bin/systemctl`: a stub that appends its arguments as one line to `calls.log`. On `start`, if the installed binary contains the text `BROKEN`, it appends the line `migrated` to the database file, standing in for a migration.
- `bin/curl`: a stub that appends its arguments to `curl.log` and exits 0 only if the installed binary exists and does not contain `BROKEN`.
- `env`: `HERMES_CHAT_DB_PATH=<tmp>/state/chat.db`, `HERMES_CHAT_TLS_MODE=autocert`, `HERMES_CHAT_TLS_DOMAIN=chat.example.com`, `HERMES_CHAT_HTTP_ADDR=:443`.
- `box.candidate(marker string) string` writes a shell script that prints `usage: hermes-chat <command>` to stderr and exits 2, with `marker` in a comment, and returns its path.
- `box.run(candidate string) (code int, stderr string)` runs `deploy/hermes-chat-upgrade` with `PATH` led by the stub directory and the four override variables set (`HERMES_CHAT_HEALTH_TIMEOUT=2`).

- `HERMES_CHAT_BIN` is `<tmp>/installed/hermes-chat`. `box.install(marker)` puts a working binary there directly. `box.path`, `box.read`, `box.write` (creating parent directories), and `box.list` (sorted directory entry names) take paths relative to the temp directory; `box.calls()` returns the lines of `calls.log`; `box.script` is the absolute path of `deploy/hermes-chat-upgrade`.

Skip the file's tests when `bash` is not on `PATH`.

```go
func TestUpgrade_FirstInstall(t *testing.T) {
	b := newBox(t)
	code, _ := b.run(b.candidate("v1"))
	assert.Equal(t, 0, code)
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.NoFileExists(t, b.path("installed/hermes-chat.previous"))
	assert.NoDirExists(t, b.path("state/backups"))
	assert.Equal(t, []string{"stop hermes-chat", "start hermes-chat"}, b.calls())
}

func TestUpgrade_KeepsPreviousBinaryAndCopiesDatabase(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	b.write("state/chat.db-wal", "wal")
	code, _ := b.run(b.candidate("v2"))
	assert.Equal(t, 0, code)
	assert.Contains(t, b.read("installed/hermes-chat"), "v2")
	assert.Contains(t, b.read("installed/hermes-chat.previous"), "v1")
	stamps := b.list("state/backups")
	require.Len(t, stamps, 1)
	assert.Regexp(t, `^\d{8}T\d{6}Z$`, stamps[0])
	assert.Equal(t, "rows", b.read("state/backups/"+stamps[0]+"/chat.db"))
	assert.Equal(t, "wal", b.read("state/backups/"+stamps[0]+"/chat.db-wal"))
}

func TestUpgrade_RollsBackBinaryAndDatabase(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.run(b.candidate("BROKEN"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rolled back to the previous version")
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.Equal(t, "rows", b.read("state/chat.db")) // the stub's "migrated" line is gone
	assert.Equal(t, []string{"stop hermes-chat", "start hermes-chat", "stop hermes-chat", "start hermes-chat"}, b.calls())
}

func TestUpgrade_FailedFirstInstallLeavesTheUnitStopped(t *testing.T) {
	b := newBox(t)
	code, stderr := b.run(b.candidate("BROKEN"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "nothing to roll back to")
	assert.Equal(t, "stop hermes-chat", b.calls()[len(b.calls())-1])
}

func TestUpgrade_RejectsCandidateThatDoesNotRun(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	for _, body := range []string{"not a program", "#!/bin/sh\nexit 0\n"} {
		p := b.path("upload")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o755))
		code, stderr := b.run(p)
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "candidate does not run on this host")
	}
	assert.Empty(t, b.calls()) // the running service was never stopped
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
}

func TestUpgrade_KeepsFiveBackups(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	for _, s := range []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z", "20260104T000000Z", "20260105T000000Z", "20260106T000000Z"} {
		b.write("state/backups/"+s+"/chat.db", "old")
	}
	code, _ := b.run(b.candidate("v2"))
	assert.Equal(t, 0, code)
	stamps := b.list("state/backups")
	require.Len(t, stamps, 5)
	assert.NotContains(t, stamps, "20260101T000000Z")
	assert.NotContains(t, stamps, "20260102T000000Z")
}

func TestUpgrade_StopsNothingWithoutADatabasePath(t *testing.T) {
	b := newBox(t)
	b.write("env", "HERMES_CHAT_TLS_MODE=off\n")
	code, stderr := b.run(b.candidate("v1"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "HERMES_CHAT_DB_PATH")
	assert.Empty(t, b.calls())
}

func TestUpgrade_HealthURL(t *testing.T) {
	b := newBox(t)
	b.run(b.candidate("v1"))
	assert.Contains(t, b.read("curl.log"), "--resolve chat.example.com:443:127.0.0.1 https://chat.example.com:443/healthz")

	b = newBox(t)
	b.write("env", "HERMES_CHAT_DB_PATH="+b.path("state/chat.db")+"\nHERMES_CHAT_TLS_MODE=off\nHERMES_CHAT_HTTP_ADDR=127.0.0.1:8080\n")
	b.run(b.candidate("v1"))
	assert.Contains(t, b.read("curl.log"), "http://127.0.0.1:8080/healthz")
	assert.NotContains(t, b.read("curl.log"), "--resolve")
}

func TestUpgrade_Usage(t *testing.T) {
	b := newBox(t)
	out, err := exec.Command("bash", b.script).CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.ExitCode())
	assert.Contains(t, string(out), "usage: hermes-chat-upgrade CANDIDATE")
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./deploy -v`
Expected: every test fails because `deploy/hermes-chat-upgrade` does not exist.

- [ ] **Step 3: Write `deploy/hermes-chat-upgrade`** to the nine numbered behaviors above.

- [ ] **Step 4: Run the tests and shellcheck**

Run: `go test ./deploy -v && shellcheck deploy/hermes-chat-upgrade`
Expected: all `PASS`; shellcheck prints nothing.

- [ ] **Step 5: Add a `lint-scripts` recipe** to the `justfile` that runs `shellcheck deploy/hermes-chat-upgrade`, make `check` depend on it, and add it to the README recipes list.

- [ ] **Step 6: Confirm the layering test still passes with the new package**

Run: `just check`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add deploy/hermes-chat-upgrade deploy/upgrade_test.go justfile README.md
git commit -m "feat(deploy): on-box upgrade script with database copy and rollback"
```

---

### Task 4: Unit, environment file, admin wrapper, and runbook

**Files:**
- Create: `deploy/hermes-chat.service`
- Create: `deploy/hermes-chat.env.example`
- Create: `deploy/hermes-chat-admin` (mode `0755`)
- Create: `docs/deploy.md`
- Modify: `justfile` (`lint-scripts` covers the new script), `README.md` (a "Deploying" section linking the runbook), `AGENTS.md` (key directories, important files, the config line), `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (new section 6.7 "Deploy")

**Interfaces:**
- Consumes: `hermes-chat-upgrade CANDIDATE` from Task 3; the three TLS variables from Task 1.
- Produces: the files Task 5 installs, at the paths in Global Constraints.

- [ ] **Step 1: Write `deploy/hermes-chat.service`**

```ini
[Unit]
Description=Hermes Chat
Documentation=https://github.com/quells-bot/hermes-chat
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=hermes-chat
Group=hermes-chat
EnvironmentFile=/etc/hermes-chat/env
ExecStart=/usr/local/bin/hermes-chat serve
Restart=on-failure
RestartSec=2
TimeoutStopSec=15
LimitNOFILE=65536

StateDirectory=hermes-chat
StateDirectoryMode=0700
WorkingDirectory=/var/lib/hermes-chat
UMask=0077

AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
```

`serve` stops on SIGTERM (grace's `StopOnInterrupt`) with a 5 s drain, so the default kill signal and a 15 s stop timeout are right.

- [ ] **Step 2: Write `deploy/hermes-chat.env.example`**

Plain `NAME=value` lines, no quotes, no `export`, because both systemd and the upgrade script read it. Comments on their own lines.

```
HERMES_CHAT_DB_PATH=/var/lib/hermes-chat/chat.db
HERMES_CHAT_HTTP_ADDR=:443
HERMES_CHAT_TLS_MODE=autocert
HERMES_CHAT_TLS_DOMAIN=chat.example.com
```

Follow these with the remaining variables commented out at their defaults, each with a one-line note.

- [ ] **Step 3: Write `deploy/hermes-chat-admin`**

Runs an admin subcommand as the service user with the service's environment: `hermes-chat-admin user add kai`. It reads `HERMES_CHAT_ENV_FILE` (default `/etc/hermes-chat/env`), exports every `HERMES_CHAT_*` line from it, and execs `runuser -u hermes-chat -- /usr/local/bin/hermes-chat "$@"`. It refuses `serve` with `use systemctl to run the server` and exit 2, and prints its usage and exits 2 with no arguments. The terminal stays attached, so the password prompt works.

- [ ] **Step 4: Lint**

Add `deploy/hermes-chat-admin` to the `lint-scripts` recipe.

Run: `just lint-scripts`
Expected: no output.

- [ ] **Step 5: Write `docs/deploy.md`**, the runbook. Every host name is `chat.example.com`, every address `203.0.113.10`. Each section is a numbered list of commands with the output that means it worked. Sections, in this order:

1. **What you need.** A Debian 13 host reachable on 443 (22 for you), a DNS name with an A record and optionally an AAAA record pointing at it, and this repository with Go and `just` on the machine you build on. Port 80 is not used.
2. **Before the first start, check DNS and IPv6.** `dig +short A` and `AAAA` must return the host's addresses. If an AAAA record exists, `ssh` to the host over IPv6 (`ssh -6`) to prove the address reaches it; Let's Encrypt validates over IPv6 when the record exists, and a record that does not reach the host makes issuance fail. Starting before DNS resolves spends Let's Encrypt's failed-validation allowance (five an hour per name).
3. **Prepare the host** (once, as root): `apt-get install -y curl ufw`; `ufw allow 22/tcp`, `ufw allow 443/tcp`, then `ufw --force enable`, in that order so the ssh session survives; `useradd --system --home-dir /var/lib/hermes-chat --shell /usr/sbin/nologin hermes-chat`; `install -d -m 0755 /etc/hermes-chat`.
4. **Copy the deploy files** from the build machine with `scp`, then on the host `install` the unit to `/etc/systemd/system/hermes-chat.service` (0644), the two scripts to `/usr/local/sbin/` (0755), and the example to `/etc/hermes-chat/env` (0644); edit the domain; `systemctl daemon-reload`; `systemctl enable hermes-chat`.
5. **Build and install.** `just build linux amd64` (or `arm64`), `scp bin/hermes-chat-linux-amd64 root@203.0.113.10:/root/`, then on the host `hermes-chat-upgrade /root/hermes-chat-linux-amd64`. The first health check is also the first TLS handshake, so it is what makes the certificate be issued; allow it up to a minute.
6. **Check it.** `curl -sI --http2 https://chat.example.com/healthz` shows `HTTP/2 200`. `systemctl status hermes-chat`. `ufw status` lists 22 and 443 for v4 and v6.
7. **Create people and agents.** `hermes-chat-admin user add NAME`, `hermes-chat-admin agent add HANDLE NAME`; the agent token is printed once.
8. **Upgrade.** Build, `scp`, `hermes-chat-upgrade`. Say what the script does, that open event streams drop and clients reconnect, and that a failed health check restores the old binary and the database as it was before the upgrade.
9. **Roll back by hand.** Stop the unit, move `hermes-chat.previous` back, copy the files from the chosen `backups/<stamp>/` over the database (removing a stale `-wal`/`-shm`), `chown hermes-chat:`, start. Warn that this discards everything written since that copy.
10. **Logs.** `journalctl -u hermes-chat -o cat -f` gives the JSON lines. Certificate trouble shows as lines containing `acme/autocert`. A public host also logs `TLS handshake error` lines from scanners asking for other names; they are expected.
11. **What lives where,** as a table of the paths in Global Constraints, and **what to back up**: the database, `attachments/`, and (to avoid re-issuing) `certs/`. The `backups/` directory holds only the last five pre-upgrade copies and is on the same disk, so it is not a backup.
12. **Running behind a reverse proxy instead.** `HERMES_CHAT_TLS_MODE=off`, a loopback `HERMES_CHAT_HTTP_ADDR`, `HERMES_CHAT_TRUSTED_PROXIES`, and the `Host` header note already in the README.

- [ ] **Step 6: Update the other documents.**
- Architecture spec, new section 6.7 "Deploy": the decisions from Global Constraints marked *(Kai)* and *(Claude's call, accepted by Kai)* as they are there, plus: a host firewall (ufw, 22 and 443) is part of the runbook; scheduled off-host backups are out of scope for stage 7.
- `README.md`: a short "Deploying" section pointing at `docs/deploy.md` and listing the files under `deploy/`.
- `AGENTS.md`: add `deploy/` and `docs/deploy.md` to Key Directories, `tls.go` and `deploy/hermes-chat-upgrade` to Important Files, mention the TLS settings in the Configuration convention, change "stages 1–6" to "stages 1–7", and add `go test ./deploy` (the upgrade script against stubbed `systemctl` and `curl`) to Testing & QA.

- [ ] **Step 7: Check that nothing names an instance**

Run: `git grep -n -I -E '([0-9]{1,3}\.){3}[0-9]{1,3}' -- deploy docs/deploy.md | grep -v -E '127\.0\.0\.1|203\.0\.113\.'`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
just check
git add deploy docs/deploy.md justfile README.md AGENTS.md docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md
git commit -m "feat(deploy): systemd unit, environment example, admin wrapper, and runbook"
```

---

### Task 5: Bring up Kai's host by following the runbook

This task changes a real server and commits nothing except corrections to the runbook. The controller supplies the host address and the domain; call them HOST and DOMAIN here and keep them out of every tracked file and commit message. The host is Debian 13, x86_64, reached as root over ssh, with a provider firewall already allowing ssh, http, https, and ping.

**The point is to test the runbook.** Do exactly what `docs/deploy.md` says, in its order. Wherever a command fails, is missing, or needs something the runbook does not say, fix the runbook first and then continue.

**Interfaces:**
- Consumes: everything from Tasks 1–4.

- [ ] **Step 1: Build**

Run: `just build linux amd64`
Expected: `bin/hermes-chat-linux-amd64` exists; `file` reports a statically linked x86-64 ELF.

- [ ] **Step 2: Runbook sections 3 and 4 on HOST.** Then verify the unit:

Run on HOST: `systemd-analyze verify /etc/systemd/system/hermes-chat.service; ufw status verbose; id hermes-chat`
Expected: no errors from `verify` (a warning that the binary does not exist yet is fine), ufw active with 22/tcp and 443/tcp for v4 and v6, and the user exists.

- [ ] **Step 3: Confirm ssh still works in a new connection** before closing the one that enabled ufw.

- [ ] **Step 4: Check DNS (runbook section 2).**

Run: `dig +short A DOMAIN; dig +short AAAA DOMAIN`

If either does not yet return HOST's addresses, **stop here**: do not start the service, report which records are missing, and leave steps 5–8 for when they resolve. Everything up to this point is safe to leave in place.

- [ ] **Step 5: Check IPv6 reaches the host**

Run: `ssh -6 root@DOMAIN true`
Expected: exit 0. If this machine has no IPv6 route, say so and check instead from HOST that the address is bound and that `ufw status` shows the v6 rules.

- [ ] **Step 6: Runbook section 5: copy the binary and run `hermes-chat-upgrade`.**

Expected: `installed`, exit 0.

- [ ] **Step 7: Runbook section 6.**

Run: `curl -sI --http2 https://DOMAIN/healthz`
Expected: first line `HTTP/2 200`.

Run on HOST: `systemctl is-active hermes-chat; systemd-analyze security hermes-chat | tail -1; ls -la /var/lib/hermes-chat`
Expected: `active`; an exposure score reported as OK or better; `chat.db`, `attachments`, and `certs` owned by `hermes-chat`.

- [ ] **Step 8: Exercise an upgrade.** Run `hermes-chat-upgrade` again with the same binary.

Expected: `upgraded; previous binary kept at /usr/local/bin/hermes-chat.previous`, one directory under `/var/lib/hermes-chat/backups/`, and step 7's curl still shows `HTTP/2 200`.

- [ ] **Step 9: Leave account creation to Kai.** Do not create users or agents; report that `hermes-chat-admin user add NAME` is the next thing for Kai to run.

- [ ] **Step 10: Commit any runbook corrections**

```bash
just check
git add docs/deploy.md deploy
git commit -m "docs(deploy): corrections from the first real install"
```

Skip the commit if nothing changed.
