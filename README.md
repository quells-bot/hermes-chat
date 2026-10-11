# Hermes Chat

Hermes Chat is a chat server written in Go, backed by SQLite. The design is in
`docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` and
`docs/superpowers/specs/2026-10-11-hermes-chat-design.md`.

## Tools

Install `just` and `sqlc` yourself. goose is used as a library, so its CLI is
not needed.

| Tool | Version |
|---|---|
| Go | 1.26.5 |
| sqlc | 1.31.1 |
| Node / npm | 24.18.1 / 11.16.0 |
| Python / uv | 3.14.4 / 0.11.31 |
| just | 1.45.0 |
| shellcheck | 0.11.0 |
| sqlite3 shell | 3.46.1 |

## Recipes

- `just generate`: run `sqlc generate` to regenerate the query code.
- `just check-generated`: run `sqlc diff`; fails if generated code is stale.
- `just vet`: run `go vet ./...`.
- `just test`: run `go test ./...`.
- `just build [goos] [goarch]`: build `bin/hermes-chat-<goos>-<goarch>` with
  `CGO_ENABLED=0`. Defaults to linux amd64.
- `just check`: `check-generated`, `vet`, and `test`.

## Configuration

Set through environment variables. Only variables with the `HERMES_CHAT_` prefix are read; unprefixed names such as `LOG_LEVEL` are ignored.

| Variable | Default | Notes |
|---|---|---|
| `HERMES_CHAT_HTTP_ADDR` | `:8080` | Address of the HTTP server. |
| `HERMES_CHAT_DEBUG_ADDR` | `127.0.0.1:6060` | Serves pprof. Must be `localhost`, a 127.0.0.0/8 address, or `::1`; anything else is rejected when `serve` starts. |
| `HERMES_CHAT_DB_PATH` | none | Path to the SQLite file, relative (resolved against the working directory) or absolute. Required. |
| `HERMES_CHAT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `HERMES_CHAT_GIN_MODE` | `release` | `debug`, `release`, or `test`; checked when `serve` starts. |
| `HERMES_CHAT_COOKIE_SECURE` | `true` | The `Secure` attribute on the session cookie. Set `false` only for plain HTTP on a development machine. |
| `HERMES_CHAT_TRUSTED_PROXIES` | empty | Comma-separated IPs or CIDRs whose `X-Forwarded-For` is believed. Needed only behind a reverse proxy; empty trusts none, so the client IP is the TCP peer. Checked when `serve` starts. |

## Commands

    hermes-chat serve                         run the HTTP server
    hermes-chat migrate                       apply database migrations
    hermes-chat user add NAME                 create a human
    hermes-chat user list                     list humans
    hermes-chat user passwd NAME              change a human's password
    hermes-chat user remove NAME              remove a human
    hermes-chat agent add HANDLE NAME         create an agent
    hermes-chat agent list                    list agents
    hermes-chat agent rotate HANDLE           issue an agent a new token
    hermes-chat agent revoke HANDLE           revoke an agent's token

With no command or an unknown one, `hermes-chat` prints usage and exits 2
without needing any configuration. `serve` and `migrate` apply migrations;
every other command requires the schema to be current, so run `migrate`
first.

`user add` and `user passwd` prompt for the password on a terminal and
otherwise read one line from standard input. `agent add` and `agent rotate`
print the token once; it cannot be shown again.

A human created before passwords existed needs `user passwd` before logging in.

## HTTP API

| Route | Auth | Purpose |
|---|---|---|
| `GET /healthz` | none | Reports whether storage is reachable. |
| `POST /api/login` | none, same-origin | Body `{"username":"…","password":"…"}`. Replies `{"id":"…","username":"…"}` and sets the `hermes_session` cookie (90 days, `HttpOnly`, `SameSite=Lax`, `Secure` unless configured off). |
| `POST /api/logout` | session cookie, same-origin | Ends the session and clears the cookie. Replies 204. |
| `GET /api/me` | session cookie | Replies `{"id":"…","username":"…"}`. |
| `GET /agent/me` | `Authorization: Bearer <token>` | Replies `{"id":"…","handle":"…","display_name":"…"}`. |

Same-origin means the `Origin` header must name this server. Errors have one
shape:

    {"error":{"code":"invalid_credentials","message":"wrong username or password"}}

| Status | `code` | Meaning |
|---|---|---|
| 400 | `bad_request` | A login body that is not a JSON object with username and password. |
| 400 | `validation` | The request was understood but a value is not acceptable; `message` says which. |
| 401 | `invalid_credentials` | Wrong username or password. |
| 401 | `unauthenticated` | No valid session or token. |
| 403 | `forbidden` | Not allowed, including a cross-origin state-changing request. |
| 404 | `not_found` | No such thing. |
| 409 | `conflict` | It already exists. |
| 429 | `throttled` | Too many attempts; `Retry-After` gives the seconds to wait. |
| 500 | `internal` | Something failed on the server; details are only in the log. |

## Adding a migration

Migrations live in `cmd/hermes-chat/internal/storage/migrations/`.

1. Create the next numbered file, for example `00002_name.sql`.
2. Start it with `-- +goose Up`, then the statements, then `-- +goose Down`
   with the statements that undo them.
3. Declare every table `STRICT`.
4. Run `just generate` so sqlc picks up the schema.

For example, `00001_humans.sql`:

    -- +goose Up
    CREATE TABLE humans (
        id         TEXT    NOT NULL PRIMARY KEY,
        username   TEXT    NOT NULL UNIQUE,
        created_at INTEGER NOT NULL
    ) STRICT;

    -- +goose Down
    DROP TABLE humans;

## Layering

Code under `cmd/hermes-chat/internal` is layered `app` to `bus` to `storage`:
each layer may import only the one below it, and `reqid` is a leaf that imports
none of them. Only `storage` may import its generated `sqlcgen` package;
`TestLayering_ThisRepository` checks this against the real import graph.
