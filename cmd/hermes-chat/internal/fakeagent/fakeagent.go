// Package fakeagent is a test-only client for the agent API. It is a leaf: it
// imports only net/http and internal/sse, and no production package may import
// it. It speaks the agent API for one agent token and drives the agent stream
// (GET /agent/events) like the Hermes plugin would.
package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/sse"
)

// Client speaks the agent API for one agent token.
type Client struct {
	BaseURL string // for example http://127.0.0.1:1234
	Token   string
	HTTP    *http.Client // nil means http.DefaultClient
}

// Stream is an open GET /agent/events response: the response to close, the
// frame decoder, and the id of the last frame read (the reconnect cursor).
type Stream struct {
	resp   *http.Response
	dec    *sse.Decoder
	lastID string
	closed bool // so Close is idempotent
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// do runs one agent API request with the bearer header and returns the status
// and raw body. err is only a transport or decode failure; a non-2xx status is
// the caller's to check, and decodeError turns it into an error.
func (c *Client) do(_ctx context.Context, method, path, body string) (int, []byte, error) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer " + c.Token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

// decodeError turns a non-2xx status into a descriptive error, preferring the
// server's error JSON when the response carried one.
func decodeError(status int, raw []byte) error {
	if len(raw) > 0 {
		var body map[string]any
		if json.Unmarshal(raw, &body) == nil {
			if e, ok := body["error"].(map[string]any); ok {
				code := ""
				if v, ok := e["code"].(string); ok {
					code = v
				}
				message := ""
				if v, ok := e["message"].(string); ok {
					message = v
				}
				return fmt.Errorf("agent API %d %s: %s", status, code, message)
			}
		}
	}
	return fmt.Errorf("agent API returned status %d", status)
}

// decodeJSON parses raw as a JSON object.
func decodeJSON(what string, raw []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", what, err)
	}
	return out, nil
}

// jq quotes s as a JSON string literal.
func jq(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// Open opens the agent's event stream. The request carries the bearer token
// and, when lastEventID is not empty, a Last-Event-ID header so the stream
// replays from there. The request uses c.HTTP with no overall timeout, because
// a stream outlives one; ctx cancellation closes the body.
func (c *Client) Open(ctx context.Context, lastEventID string) (*Stream, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/agent/events", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer " + c.Token)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, decodeError(resp.StatusCode, raw)
	}
	return &Stream{resp: resp, dec: sse.NewDecoder(resp.Body)}, nil
}

// Next returns the next event frame, skipping comment-only (heartbeat) frames
// and honoring ctx: when the context ends, the body is closed and an error is
// returned. The frame's id becomes the stream cursor.
func (s *Stream) Next(ctx context.Context) (sse.Frame, error) {
	type result struct {
		f   sse.Frame
		err error
	}
	done := make(chan result, 1)
	go func() {
		for {
			f, err := s.dec.Next()
			if err != nil || !(f.Comment != "" && f.ID == "" && f.Event == "" && f.Data == "") {
				done <- result{f, err}
				return
			}
		}
	}()
	select {
	case r := <-done:
		s.lastID = r.f.ID
		return r.f, r.err
	case <-ctx.Done():
		s.Close() // unblocks the reader so it can exit
		select {
		case r := <-done:
			return r.f, r.err
		case <-time.After(500 * time.Millisecond):
			return sse.Frame{}, ctx.Err()
		}
	}
}

// Cursor is the last frame's id, or "" before any frame was read.
func (s *Stream) Cursor() string {
	return s.lastID
}

// Close closes the response body, ending the stream. Closing twice is a no-op.
func (s *Stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.resp.Body.Close()
}

// Rooms lists the agent's rooms.
func (c *Client) Rooms(ctx context.Context) ([]map[string]any, error) {
	status, raw, err := c.do(ctx, "GET", "/agent/rooms", "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, decodeError(status, raw)
	}
	body, err := decodeJSON("list rooms", raw)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, r := range body["rooms"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out, nil
}

// PostMessage posts a message into a room the agent belongs to and returns the
// new message's id. replyToID "" means no reply.
func (c *Client) PostMessage(ctx context.Context, roomID, text, replyToID string) (string, error) {
	reply := "null"
	if replyToID != "" {
		reply = jq(replyToID)
	}
	body := `{"text":` + jq(text) + `,"reply_to_id":` + reply + `}`
	status, raw, err := c.do(ctx, "POST", "/agent/rooms/"+roomID+"/messages", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated {
		return "", decodeError(status, raw)
	}
	msg, err := decodeJSON("post message", raw)
	if err != nil {
		return "", err
	}
	return msg["id"].(string), nil
}

// EditMessage rewrites one of the agent's own messages. final reports whether
// this is the last edit of the stream.
func (c *Client) EditMessage(ctx context.Context, messageID, text string, final bool) error {
	body := fmt.Sprintf(`{"text":%s,"final":%t}`, jq(text), final)
	status, raw, err := c.do(ctx, "PUT", "/agent/messages/"+messageID, body)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return decodeError(status, raw)
	}
	return nil
}

// Prompt posts the agent's question as a prompt message and returns its id.
func (c *Client) Prompt(ctx context.Context, roomID, kind, text string, choices []string) (string, error) {
	cs := "null"
	if choices != nil {
		var parts []string
		for _, ch := range choices {
			parts = append(parts, jq(ch))
		}
		cs = "[" + strings.Join(parts, ",") + "]"
	}
	body := `{"kind":` + jq(kind) + `,"text":` + jq(text) + `,"choices":` + cs + `}`
	status, raw, err := c.do(ctx, "POST", "/agent/rooms/"+roomID+"/prompts", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated {
		return "", decodeError(status, raw)
	}
	msg, err := decodeJSON("post prompt", raw)
	if err != nil {
		return "", err
	}
	return msg["id"].(string), nil
}

// ExpirePrompt closes a prompt the asking agent gave up on.
func (c *Client) ExpirePrompt(ctx context.Context, messageID string) error {
	status, raw, err := c.do(ctx, "POST", "/agent/prompts/"+messageID+"/expire", "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return decodeError(status, raw)
	}
	return nil
}

// SuggestNote suggests an entry for the note of the human who wrote the
// delivered message. Both 200 (already suggested) and 201 (fresh) succeed.
func (c *Client) SuggestNote(ctx context.Context, messageID, text string) (string, error) {
	body := `{"text":` + jq(text) + `}`
	status, raw, err := c.do(ctx, "POST", "/agent/deliveries/"+messageID+"/note-entries", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return "", decodeError(status, raw)
	}
	entry, err := decodeJSON("suggest note", raw)
	if err != nil {
		return "", err
	}
	return entry["id"].(string), nil
}

// Upload stores one file the agent uploads to a room and returns its id. The
// body is a reader, not JSON, so it cannot use do.
func (c *Client) Upload(ctx context.Context, roomID, name string, body io.Reader) (string, error) {
	req, err := http.NewRequest("POST", c.BaseURL+"/agent/rooms/"+roomID+"/attachments?name="+url.QueryEscape(name), body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", decodeError(resp.StatusCode, raw)
	}
	att, err := decodeJSON("upload", raw)
	if err != nil {
		return "", err
	}
	return att["id"].(string), nil
}

// PostMessageWith posts a message carrying the given attachment ids.
func (c *Client) PostMessageWith(ctx context.Context, roomID, text string, attachmentIDs []string) (string, error) {
	var parts []string
	for _, id := range attachmentIDs {
		parts = append(parts, jq(id))
	}
	body := `{"text":` + jq(text) + `,"attachment_ids":[` + strings.Join(parts, ",") + `]}`
	status, raw, err := c.do(ctx, "POST", "/agent/rooms/"+roomID+"/messages", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated {
		return "", decodeError(status, raw)
	}
	msg, err := decodeJSON("post message with attachments", raw)
	if err != nil {
		return "", err
	}
	return msg["id"].(string), nil
}

// turnReport runs one of the turn reports, which take no body.
func (c *Client) turnReport(ctx context.Context, state, messageID string) error {
	status, raw, err := c.do(ctx, "POST", "/agent/deliveries/"+messageID+"/"+state, "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return decodeError(status, raw)
	}
	return nil
}

// TurnWorking marks the delivery of the delivered message working.
func (c *Client) TurnWorking(ctx context.Context, messageID string) error {
	return c.turnReport(ctx, "working", messageID)
}

// TurnDone reports the turn done.
func (c *Client) TurnDone(ctx context.Context, messageID string) error {
	return c.turnReport(ctx, "done", messageID)
}

// TurnFailed reports the turn failed.
func (c *Client) TurnFailed(ctx context.Context, messageID string) error {
	return c.turnReport(ctx, "failed", messageID)
}

// Remark records a remark the agent made while working on a delivered message.
func (c *Client) Remark(ctx context.Context, messageID, text string) error {
	body := `{"text":` + jq(text) + `}`
	status, raw, err := c.do(ctx, "POST", "/agent/deliveries/"+messageID+"/remarks", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return decodeError(status, raw)
	}
	return nil
}

// ToolStart records a tool call the agent started; preview and arguments are
// optional ("" to omit a value, the body still carries them empty). It returns
// the activity's id.
func (c *Client) ToolStart(ctx context.Context, messageID, tool, preview, arguments string) (string, error) {
	body := `{"tool":` + jq(tool) + `,"preview":` + jq(preview) + `,"arguments":` + jq(arguments) + `}`
	status, raw, err := c.do(ctx, "POST", "/agent/deliveries/"+messageID+"/tools", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated {
		return "", decodeError(status, raw)
	}
	act, err := decodeJSON("start tool", raw)
	if err != nil {
		return "", err
	}
	return act["id"].(string), nil
}

// ToolEnd records a tool call's result.
func (c *Client) ToolEnd(ctx context.Context, activityID string, durationMs int64, succeeded bool) error {
	body := fmt.Sprintf(`{"duration_ms":%d,"succeeded":%t}`, durationMs, succeeded)
	status, raw, err := c.do(ctx, "PUT", "/agent/activities/"+activityID, body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return decodeError(status, raw)
	}
	return nil
}