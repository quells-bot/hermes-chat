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

Set through environment variables.

| Variable | Default | Notes |
|---|---|---|
| `HERMES_CHAT_HTTP_ADDR` | `:8080` | Address of the HTTP server. |
| `HERMES_CHAT_DEBUG_ADDR` | `127.0.0.1:6060` | Serves pprof. Must be `localhost`, a 127.0.0.0/8 address, or `::1`; anything else is rejected at start-up. |
| `HERMES_CHAT_DB_PATH` | none | Path to the SQLite file. Required. |
| `HERMES_CHAT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `HERMES_CHAT_GIN_MODE` | `release` | `debug`, `release`, or `test`. |

## Commands

    hermes-chat serve            run the HTTP server
    hermes-chat migrate          apply database migrations
    hermes-chat user add NAME    create a user
    hermes-chat user list        list users

With no command or an unknown one, `hermes-chat` prints usage and exits 2
without needing any configuration. `serve`, `user add`, and `user list` expect
the database to be migrated already; run `migrate` first.

`user add` creates a human without a password. Passwords arrive in a later
stage, so login does not work yet.

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
