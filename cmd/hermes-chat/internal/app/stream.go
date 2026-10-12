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
	"github.com/quells-bot/hermes-chat/internal/sse"
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

// eventFrame is the frame for one bus event, as a human subscriber sees it.
func (h *controller) eventFrame(ctx context.Context, e bus.Event) (sse.Frame, error) {
	var body map[string]any
	switch {
	case e.Kind == bus.EventMessagePosted && e.Message != nil:
		body = map[string]any{"message": messageJSON(*e.Message)}
	case e.Kind == bus.EventRoomChanged && e.Room != nil:
		body = map[string]any{"room": roomJSON(*e.Room)}
	case e.Kind == bus.EventDeliveryChanged && e.Message != nil:
		body = map[string]any{"message": messageJSON(*e.Message)}
	case e.Kind == bus.EventActivityPosted && e.Activity != nil:
		act, err := h.activityJSON(ctx, *e.Activity)
		if err != nil {
			return sse.Frame{}, err
		}
		body = map[string]any{"message_id": e.Activity.MessageID.String(), "activity": act}
	case e.Kind == bus.EventAgentPresence && e.Agent != nil:
		body = map[string]any{"agent": agentJSON(*e.Agent), "connected": e.Connected}
	case e.Kind == bus.EventMessageEdited && e.Message != nil:
		// the finalized message, from the log
		body = map[string]any{"message": messageJSON(*e.Message)}
	case e.Kind == bus.EventPromptChanged && e.Message != nil:
		body = map[string]any{"message": messageJSON(*e.Message)}
	default:
		return sse.Frame{}, fmt.Errorf("event %d: nothing to send for kind %q", e.ID, e.Kind)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return sse.Frame{}, fmt.Errorf("event %d: %w", e.ID, err)
	}
	return sse.Frame{ID: strconv.FormatInt(e.ID, 10), Event: string(e.Kind), Data: string(data)}, nil
}

// agentEventFrame is the frame for one event the agent stream shows: a delivery,
// a stop, or a steer. Agents never see the other kinds.
func agentEventFrame(e bus.Event) (sse.Frame, error) {
	var body map[string]any
	switch {
	case e.Kind == bus.EventDelivery && e.Message != nil && e.Room != nil:
		context := make([]any, 0, len(e.Context))
		for _, cm := range e.Context {
			context = append(context, agentMessageJSON(cm))
		}
		var note *string
		if e.Note != "" {
			note = &e.Note
		}
		body = map[string]any{
			"message": agentMessageJSON(*e.Message),
			"room":    agentRoomJSON(*e.Room),
			"context": context,
			"note":    note,
		}
	case e.Kind == bus.EventControlStop, e.Kind == bus.EventControlPause:
		body = map[string]any{"room_id": e.RoomID.String()}
	case e.Kind == bus.EventControlSteer && e.Message != nil:
		body = map[string]any{"room_id": e.RoomID.String(), "message": agentMessageJSON(*e.Message)}
	case e.Kind == bus.EventPromptAnswered && e.Message != nil && e.Message.Prompt != nil:
		body = map[string]any{
			"room_id":    e.RoomID.String(),
			"message_id": e.Message.ID.String(),
			"answer":     e.Message.Prompt.Answer,
		}
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
	// is dropped. The deadline is cleared after each flush: over HTTP/2 it is
	// a timer that resets the stream when it fires, written to or not.
	rc := http.NewResponseController(c.Writer)
	write := func(f sse.Frame) error {
		if err := rc.SetWriteDeadline(time.Now().Add(h.opts.StreamWriteTimeout)); err != nil {
			return err
		}
		return sse.Write(c.Writer, f)
	}
	flush := func() error {
		if err := rc.Flush(); err != nil {
			return err
		}
		return rc.SetWriteDeadline(time.Time{})
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
	if write(sse.Frame{ID: cursor, Event: "ready", Data: `{"cursor":` + cursor + `}`}) != nil || flush() != nil {
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
				f, err := h.eventFrame(ctx, e)
				if err != nil {
					slog.ErrorContext(ctx, "stream event failed", "error", err)
					return
				}
				if write(f) != nil {
					return
				}
			}
			if flush() != nil {
				return
			}
		}

		select {
		case _, open := <-sub.Wake():
			if !open { // the hub stopped
				return
			}
		case ed := <-sub.Edits():
			// a live edit, pushed in memory so it reaches the browser at once
			f := sse.Frame{ID: strconv.FormatInt(sub.Cursor(), 10), Event: "message.edited",
				Data: fmt.Sprintf(`{"message_id":%q,"text":%s,"final":false}`, ed.MessageID.String(), jsonString(ed.Text))}
			if write(f) != nil || flush() != nil {
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
			if write(sse.Frame{Comment: "hb"}) != nil || flush() != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// AgentEvents streams the agent's deliveries and control events as Server-Sent
// Events until the client leaves, the token stops working, or the server shuts
// down. It mirrors Events except that the heartbeat re-checks the bearer token,
// the exit path reports the agent gone, and the frames are the agent's own.
func (h *controller) AgentEvents(c *gin.Context) {
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
	defer func() {
		sub.Close()
		// The context is already over on a client disconnect, the dominant
		// close path, so run the presence-off write detached from its
		// cancellation: it must still reach the log, or browsers miss it.
		if err := h.svc.AgentGone(context.WithoutCancel(ctx), actor); err != nil {
			slog.ErrorContext(ctx, "agent gone", "error", err)
		}
	}()

	// As in Events: a deadline per write, cleared after each flush.
	rc := http.NewResponseController(c.Writer)
	write := func(f sse.Frame) error {
		if err := rc.SetWriteDeadline(time.Now().Add(h.opts.StreamWriteTimeout)); err != nil {
			return err
		}
		return sse.Write(c.Writer, f)
	}
	flush := func() error {
		if err := rc.Flush(); err != nil {
			return err
		}
		return rc.SetWriteDeadline(time.Time{})
	}
	if err := rc.SetWriteDeadline(time.Now().Add(h.opts.StreamWriteTimeout)); err != nil {
		writeError(c, fmt.Errorf("agent events: set write deadline: %w", err))
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	slog.InfoContext(ctx, "stream opened", "actor", actor.String(), "cursor", sub.Cursor())

	cursor := strconv.FormatInt(sub.Cursor(), 10)
	if write(sse.Frame{ID: cursor, Event: "ready", Data: `{"cursor":` + cursor + `}`}) != nil || flush() != nil {
		return
	}

	token := bearer(c) // AgentAuth has already vouched for it
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
				f, err := agentEventFrame(e)
				if err != nil {
					slog.ErrorContext(ctx, "stream event failed", "error", err)
					return
				}
				if write(f) != nil {
					return
				}
			}
			if flush() != nil {
				return
			}
		}

		select {
		case _, open := <-sub.Wake():
			if !open { // the hub stopped, or the stream was replaced
				return
			}
		case <-heartbeat.C:
			// The token may have been revoked since the stream opened.
			current, err := h.svc.ResolveAgentToken(ctx, token)
			if err != nil {
				if !errors.Is(err, bus.ErrUnauthenticated) && !canceled(err) {
					slog.ErrorContext(ctx, "stream session check failed", "error", err)
				}
				return
			}
			if current != actor {
				return
			}
			if write(sse.Frame{Comment: "hb"}) != nil || flush() != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
