// Command fakeagent connects to a Hermes Chat server as one agent, prints every
// event it is sent, and answers each delivery by echoing it. It exists to try a
// server by hand; it is not the Hermes plugin.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quells-bot/hermes-chat/internal/fakeagent"
)

const usage = `usage: fakeagent

Reads its settings from the environment:
  FAKEAGENT_URL    base URL of the server, for example https://chat.example.com
  FAKEAGENT_TOKEN  the agent's token, as printed by "hermes-chat agent add"

Prints each event as a JSON line on standard output and answers every
delivery with "echo: <text>". Status goes to standard error.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Second))
}

// run streams until ctx ends and returns the process exit code. A stream that
// drops is reopened from the cursor after retry; a failure to open the very
// first stream is fatal, since it is most likely a wrong URL or token.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, retry time.Duration) int {
	url, token := getenv("FAKEAGENT_URL"), getenv("FAKEAGENT_TOKEN")
	if len(args) > 0 || url == "" || token == "" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	a := &agent{client: &fakeagent.Client{BaseURL: url, Token: token}, out: json.NewEncoder(stdout), log: log, seen: map[string]bool{}}

	for {
		err := a.stream(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil && !a.opened {
			fmt.Fprintf(stderr, "fakeagent: %v\n", err)
			return 1
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(retry):
		}
	}
}

type agent struct {
	client *fakeagent.Client
	out    *json.Encoder
	log    *slog.Logger
	cursor string          // id of the last event read, sent back on reconnect
	seen   map[string]bool // message ids already answered; delivery is at least once
	opened bool            // a stream has been opened at least once
}

// stream opens one event stream and handles frames until it ends.
func (a *agent) stream(ctx context.Context) error {
	s, err := a.client.Open(ctx, a.cursor)
	if err != nil {
		if a.opened {
			a.log.Warn("stream did not open", "error", err.Error())
		}
		return err
	}
	defer s.Close()
	a.opened = true
	start := time.Now()
	a.log.Info("stream open", "cursor", a.cursor)
	for {
		f, err := s.Next(ctx)
		if err != nil {
			if ctx.Err() == nil {
				a.log.Warn("stream closed", "open_for", time.Since(start).Round(time.Millisecond).String(), "error", err.Error())
			}
			return err
		}
		if f.ID != "" {
			a.cursor = f.ID
		}
		data := json.RawMessage(f.Data)
		if !json.Valid(data) {
			data, _ = json.Marshal(f.Data)
		}
		_ = a.out.Encode(struct {
			Event string          `json:"event"`
			ID    string          `json:"id"`
			Data  json.RawMessage `json:"data"`
		}{f.Event, f.ID, data})
		if f.Event == "delivery" {
			a.answer(ctx, f.Data)
		}
	}
}

// answer runs one turn for a delivery: working, an echo reply, done.
func (a *agent) answer(ctx context.Context, data string) {
	var d struct {
		Message struct {
			ID     string `json:"id"`
			RoomID string `json:"room_id"`
			Text   string `json:"text"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &d); err != nil || d.Message.ID == "" {
		a.log.Warn("delivery not understood", "data", data)
		return
	}
	m := d.Message
	if a.seen[m.ID] {
		return
	}
	a.seen[m.ID] = true
	err := a.client.TurnWorking(ctx, m.ID)
	if err == nil {
		_, err = a.client.PostMessage(ctx, m.RoomID, "echo: "+m.Text, m.ID)
	}
	if err == nil {
		err = a.client.TurnDone(ctx, m.ID)
	}
	if err != nil {
		a.log.Warn("turn failed", "message", m.ID, "error", err.Error())
		return
	}
	a.log.Info("answered", "message", m.ID)
}
