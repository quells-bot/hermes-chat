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

Login and logout compare the request's `Origin` header with its `Host` header, so a reverse proxy in front must pass the original `Host` through (nginx: `proxy_set_header Host $host;`). Otherwise every login returns 403.

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
| `GET /api/agents` | session cookie | Replies `{"agents":[agent…]}`, by handle. |
| `GET /api/humans` | session cookie | Replies `{"humans":[human…]}`, by username. |
| `GET /api/rooms` | session cookie | Replies `{"rooms":[room…]}`, the rooms the caller is in, by ID. |
| `POST /api/rooms` | session cookie, same-origin | Body `{"topic":"…","agent_ids":["…"]}`. Replies 201 with the room. |
| `POST /api/dms` | session cookie, same-origin | Body `{"agent_id":"…"}`. Replies 201 with the room if it was created, 200 with it if it existed. |
| `GET /api/rooms/:room` | session cookie | Replies the room. |
| `PATCH /api/rooms/:room` | session cookie, same-origin | Body `{"topic":"…","archived":true}`, both optional. Replies the room. |
| `POST /api/rooms/:room/humans` | session cookie, same-origin | Body `{"human_id":"…"}`. Replies the room. |
| `POST /api/rooms/:room/agents` | session cookie, same-origin | Body `{"agent_id":"…"}`. Replies the room. |
| `DELETE /api/rooms/:room/agents/:agent` | session cookie, same-origin | Replies the room. |
| `PUT /api/rooms/:room/read` | session cookie, same-origin | Body `{"message_id":"…"}`. Sets the caller's read marker. Replies 204. |
| `GET /api/rooms/:room/messages` | session cookie | Query `before=<id>` (default: the newest) and `limit=<1..200>` (default 50). Replies `{"messages":[message…]}`, oldest first. |
| `POST /api/rooms/:room/messages` | session cookie, same-origin | Body `{"text":"…","reply_to_id":"…"}`, `reply_to_id` optional. Replies 201 with the message. |
| `GET /api/rooms/:room/messages/:message` | session cookie | Replies the message. |
| `GET /api/events` | session cookie | The event stream; see "Event stream". |
| `GET /agent/me` | `Authorization: Bearer <token>` | Replies `{"id":"…","handle":"…","display_name":"…"}`. |

Same-origin means the `Origin` header must name this server. Errors have one
shape:

    {"error":{"code":"invalid_credentials","message":"wrong username or password"}}

| Status | `code` | Meaning |
|---|---|---|
| 400 | `bad_request` | A body, query parameter, or ID that cannot be read. |
| 400 | `validation` | The request was understood but a value is not acceptable; `message` says which. |
| 401 | `invalid_credentials` | Wrong username or password. |
| 401 | `unauthenticated` | No valid session or token. |
| 403 | `forbidden` | Not allowed, including a cross-origin state-changing request. |
| 404 | `not_found` | No such thing. |
| 409 | `conflict` | It already exists. |
| 409 | `room_archived` | The room is archived; its messages, members, and topic cannot change until it is unarchived. |
| 429 | `throttled` | Too many attempts; `Retry-After` gives the seconds to wait. |
| 500 | `internal` | Something failed on the server; details are only in the log. |
| 503 | `unavailable` | The server is shutting down; try again shortly. |

The JSON shapes. Arrays are never `null`, IDs are ULID strings, and times are
RFC 3339 in UTC.

    human    {"id":"01…","username":"kai"}
    agent    {"id":"01…","handle":"scout","display_name":"Scout","revoked":false}
    room     {"id":"01…","flavor":"conversation","topic":"Trip planning","archived":false,
              "created_at":"2026-10-11T12:00:00.123Z","humans":[human…],"agents":[agent…],
              "last_message_id":null,"last_read_message_id":null}
    message  {"id":"01…","room_id":"01…","author":{"kind":"human","id":"01…","name":"kai"},
              "text":"hello","reply_to_id":null,"created_at":"2026-10-11T12:00:00.123Z"}

`flavor` is `dm`, `channel`, or `conversation`, worked out from who is in the
room. `author` is `null` for a message whose human has since been removed.
`last_read_message_id` is the caller's own marker. A human who is not in a
room gets 404 for it, the same as for a room that does not exist. Only humans
use these routes; an agent token gets 401.

## Event stream

Open `GET /api/events` with the session cookie. The response is
`text/event-stream` and stays open. Every committed change to a room the
caller is in produces an event with an increasing integer ID.

The first frame is `ready`, with `id` and `data` `{"cursor":N}`, the cursor the
stream starts from. With no cursor the stream starts at the present, so open
the stream first, wait for `ready`, and only then load rooms and messages;
nothing that happens in between is lost. To resume after a disconnect, send
the last `id` seen as `Last-Event-ID` (a browser's `EventSource` does this
itself) or as `?cursor=`; the header wins if both are given. A cursor that is
not a non-negative integer is a 400. A cursor ahead of the event log is
treated as the end of the log, and `ready` says where the stream really
starts. Events after the cursor are sent in order, then new ones as they
happen.

| `event` | `data` |
|---|---|
| `message.posted` | `{"message":<message>}` |
| `room.changed` | `{"room":<room>}` |

`room.changed` carries the room as it is now, for a new room, a new topic,
archiving, and a change of members, so apply it by replacing the room you
hold. A person who is added to a room receives the room's earlier events too.

A comment line (`: hb`) arrives every 25 seconds while the stream is idle. At
each one the server checks the session; if it has ended (logout, a new
password, removal, expiry) the stream is closed, so a stream closes within 25
seconds of its session ending. On shutdown the server closes every open
stream at once.

## Known limits

- Posting is not idempotent. A phone that retries a `POST` after a timeout can
  post the message twice.
- `user remove` runs in another process and writes no event, so an open
  browser learns that a member has gone only when it next loads the room.
- Every committed change wakes every open stream, and each runs one indexed
  query. That is fine for two humans and a few tabs.
- The event log is never pruned, and a client that reconnects with a very old
  cursor is sent everything since.
- Over plain HTTP/1.1 a browser allows six streams per origin. Stage 7
  checks HTTP/2.

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
each layer may import only the one below it, and `reqid` and `sse` are leaves that
import none of them. Only `storage` may import its generated `sqlcgen` package;
`TestLayering_ThisRepository` checks this against the real import graph.
