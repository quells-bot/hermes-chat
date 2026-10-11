package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/sse"
)

const (
	// DefaultHeartbeatInterval is how often an idle stream is checked and kept warm.
	DefaultHeartbeatInterval = 25 * time.Second
	// DefaultStreamWriteTimeout is how long one write to a stream may take.
	DefaultStreamWriteTimeout = 10 * time.Second
)

// streamCursor reads where the stream starts: the Last-Event-ID header if it
// is not empty, else the cursor query parameter, else bus.FromNow. The header
// wins because a browser's EventSource keeps its original URL when it
// reconnects. On a bad value it answers badRequest and returns false.
func streamCursor(c *gin.Context) (int64, bool) {
	value := c.GetHeader("Last-Event-ID")
	if value == "" {
		var present bool
		if value, present = c.GetQuery("cursor"); !present {
			return bus.FromNow, true
		}
	}
	// 63 bits: digits only, and no more than an int64 holds.
	n, err := strconv.ParseUint(value, 10, 63)
	if err != nil {
		badRequest(c, "cursor must be a non-negative integer")
		return 0, false
	}
	return int64(n), true
}

// canceled reports whether err is the request's own cancellation: the client
// leaving while the stream was reading, which is not a failure.
func canceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// eventFrame is the frame for one bus event.
func eventFrame(e bus.Event) (sse.Frame, error) {
	var body map[string]any
	switch {
	case e.Kind == bus.EventMessagePosted && e.Message != nil:
		body = map[string]any{"message": messageJSON(*e.Message)}
	case e.Kind == bus.EventRoomChanged && e.Room != nil:
		body = map[string]any{"room": roomJSON(*e.Room)}
	default:
		return sse.Frame{}, fmt.Errorf("event %d: nothing to send for kind %q", e.ID, e.Kind)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return sse.Frame{}, fmt.Errorf("event %d: %w", e.ID, err)
	}
	return sse.Frame{ID: strconv.FormatInt(e.ID, 10), Event: string(e.Kind), Data: string(data)}, nil
}

// Events streams the caller's events as Server-Sent Events until the client
// leaves, the session ends, or the server shuts down.
func (h *controller) Events(c *gin.Context) {
	ctx := c.Request.Context()
	actor := actorFrom(c)
	after, ok := streamCursor(c)
	if !ok {
		return
	}
	sub, err := h.svc.Subscribe(ctx, actor, after)
	if err != nil {
		writeError(c, err)
		return
	}
	defer sub.Close()

	// Each write gets its own deadline, replacing the server's for the whole
	// response: the stream outlives that one, and a client that stops reading
	// is dropped.
	rc := http.NewResponseController(c.Writer)
	write := func(f sse.Frame) error {
		if err := rc.SetWriteDeadline(time.Now().Add(h.opts.StreamWriteTimeout)); err != nil {
			return err
		}
		return sse.Write(c.Writer, f)
	}
	if err := rc.SetWriteDeadline(time.Now().Add(h.opts.StreamWriteTimeout)); err != nil {
		writeError(c, fmt.Errorf("events: set write deadline: %w", err))
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	slog.InfoContext(ctx, "stream opened", "actor", actor.String(), "cursor", sub.Cursor())

	cursor := strconv.FormatInt(sub.Cursor(), 10)
	if write(sse.Frame{ID: cursor, Event: "ready", Data: `{"cursor":` + cursor + `}`}) != nil || rc.Flush() != nil {
		return
	}

	token, _ := c.Cookie(SessionCookie) // HumanAuth has already vouched for it
	heartbeat := time.NewTicker(h.opts.HeartbeatInterval)
	defer heartbeat.Stop()

	for {
		// Drain the log before every wait, the first included: an event
		// committed while subscribing rings nobody.
		for {
			events, err := sub.Poll(ctx)
			if err != nil {
				if !canceled(err) {
					slog.ErrorContext(ctx, "stream poll failed", "error", err)
				}
				return
			}
			if len(events) == 0 {
				break
			}
			for _, e := range events {
				f, err := eventFrame(e)
				if err != nil {
					slog.ErrorContext(ctx, "stream event failed", "error", err)
					return
				}
				if write(f) != nil {
					return
				}
			}
			if rc.Flush() != nil {
				return
			}
		}

		select {
		case _, open := <-sub.Wake():
			if !open { // the hub stopped
				return
			}
		case <-heartbeat.C:
			// The session may have ended since the stream opened.
			current, err := h.svc.ResolveSession(ctx, token)
			if err != nil {
				if !errors.Is(err, bus.ErrUnauthenticated) && !canceled(err) {
					slog.ErrorContext(ctx, "stream session check failed", "error", err)
				}
				return
			}
			if current != actor {
				return
			}
			if write(sse.Frame{Comment: "hb"}) != nil || rc.Flush() != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
