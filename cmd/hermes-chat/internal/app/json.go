package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

type humanBody struct {
	ID       ulid.ULID `json:"id"`
	Username string    `json:"username"`
}

type agentBody struct {
	ID          ulid.ULID `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"display_name"`
	Revoked     bool      `json:"revoked"`
}

// roomAgentBody is one agent in a room, as a human sees it: the agent plus its
// live-stream connection state. agentBody itself stays plain for the agent
// listings and activities that carry no presence.
type roomAgentBody struct {
	agentBody
	Connected bool `json:"connected"`
}

type roomBody struct {
	ID                ulid.ULID       `json:"id"`
	Flavor            string          `json:"flavor"`
	Topic             string          `json:"topic"`
	Archived          bool            `json:"archived"`
	CreatedAt         time.Time       `json:"created_at"`
	Humans            []humanBody     `json:"humans"`
	Agents            []roomAgentBody `json:"agents"`
	LastMessageID     *ulid.ULID      `json:"last_message_id"`
	LastReadMessageID *ulid.ULID      `json:"last_read_message_id"`
	Paused            bool            `json:"paused"`
	PauseReason       *string         `json:"pause_reason"` // "human" or "turn_cap" while paused
	TurnCap           int             `json:"turn_cap"`
	Halted            bool            `json:"halted"`
}

type authorBody struct {
	Kind bus.ActorKind `json:"kind"`
	ID   ulid.ULID     `json:"id"`
	Name string        `json:"name"`
}

// deliveryBody is one agent's copy of a message, in the message object.
type deliveryBody struct {
	Agent agentBody `json:"agent"`
	State string    `json:"state"`
}

type messageBody struct {
	ID         ulid.ULID      `json:"id"`
	RoomID     ulid.ULID      `json:"room_id"`
	Author     *authorBody    `json:"author"`
	Text       string         `json:"text"`
	ReplyToID  *ulid.ULID     `json:"reply_to_id"`
	Deliveries []deliveryBody `json:"deliveries"` // never null; [] for an agent's own messages
	CreatedAt  time.Time      `json:"created_at"`
}

type agentRoomBody struct {
	ID     ulid.ULID   `json:"id"`
	Flavor string      `json:"flavor"`
	Topic  string      `json:"topic"`
	Humans []humanBody `json:"humans"`
	Paused bool        `json:"paused"` // the room is paused or everything is stopped
}

type agentAuthorBody struct {
	Kind  bus.ActorKind `json:"kind"`
	ID    ulid.ULID     `json:"id"`
	Name  string        `json:"name"`
	IsBot bool          `json:"is_bot"`
}

type agentMessageBody struct {
	ID                ulid.ULID        `json:"id"`
	RoomID            ulid.ULID        `json:"room_id"`
	Author            *agentAuthorBody `json:"author"`
	Text              string           `json:"text"`
	ReplyToMessageID  *ulid.ULID       `json:"reply_to_message_id"`
	ReplyToText       *string          `json:"reply_to_text"`
	ReplyToAuthorName *string          `json:"reply_to_author_name"`
	CreatedAt         time.Time        `json:"created_at"`
}

type activityBody struct {
	ID         ulid.ULID        `json:"id"`
	MessageID  ulid.ULID        `json:"message_id"`
	Agent      agentBody        `json:"agent"`
	Kind       bus.ActivityKind `json:"kind"`
	Text       string           `json:"text"`
	Tool       string           `json:"tool"`
	Preview    string           `json:"preview"`
	Arguments  string           `json:"arguments"`
	DurationMs *int64           `json:"duration_ms"`
	Succeeded  *bool            `json:"succeeded"`
	CreatedAt  time.Time        `json:"created_at"`
}

// optionalID is nil for a zero ID, which JSON writes as null.
func optionalID(id ulid.ULID) *ulid.ULID {
	if id.IsZero() {
		return nil
	}
	return &id
}

func humanJSON(h bus.Human) humanBody {
	return humanBody{ID: h.ID, Username: h.Username}
}

func agentJSON(a bus.Agent) agentBody {
	return agentBody{ID: a.ID, Handle: a.Handle, DisplayName: a.DisplayName, Revoked: a.Revoked()}
}

// roomJSON is the room object; its arrays are never null.
func roomJSON(r bus.Room) any {
	out := roomBody{
		ID: r.ID, Flavor: string(r.Flavor), Topic: r.Topic, Archived: r.Archived(), CreatedAt: r.CreatedAt,
		Humans: make([]humanBody, 0, len(r.Humans)), Agents: make([]roomAgentBody, 0, len(r.Agents)),
		LastMessageID: optionalID(r.LastMessageID), LastReadMessageID: optionalID(r.LastReadMessageID),
		Paused: r.Paused(), TurnCap: r.TurnCap, Halted: r.Halted,
	}
	if r.Paused() {
		reason := string(r.PauseReason)
		out.PauseReason = &reason
	}
	for _, h := range r.Humans {
		out.Humans = append(out.Humans, humanJSON(h))
	}
	for _, ra := range r.Agents {
		out.Agents = append(out.Agents, roomAgentBody{agentBody: agentJSON(ra.Agent), Connected: ra.Connected})
	}
	return out
}

// messageJSON is the message object; author is null once its human was removed.
// The deliveries array is never nil: an agent's own messages and any message
// with no waiting copies carry [].
func messageJSON(m bus.Message) any {
	out := messageBody{ID: m.ID, RoomID: m.RoomID, Text: m.Text, ReplyToID: optionalID(m.ReplyToID), CreatedAt: m.CreatedAt,
		Deliveries: make([]deliveryBody, 0, len(m.Deliveries))}
	if m.Author != nil {
		out.Author = &authorBody{Kind: m.Author.Kind, ID: m.Author.ID, Name: m.Author.Name}
	}
	for _, d := range m.Deliveries {
		out.Deliveries = append(out.Deliveries, deliveryBody{Agent: agentJSON(d.Agent), State: string(d.State)})
	}
	return out
}

// agentRoomJSON is the room as an agent sees it: members, flavor, and topic.
// Its arrays are never null.
func agentRoomJSON(r bus.Room) any {
	out := agentRoomBody{ID: r.ID, Flavor: string(r.Flavor), Topic: r.Topic, Humans: make([]humanBody, 0, len(r.Humans)),
		Paused: r.Paused() || r.Halted}
	for _, h := range r.Humans {
		out.Humans = append(out.Humans, humanJSON(h))
	}
	return out
}

// agentMessageJSON is the message an agent sees. is_bot is true exactly when the
// author is an agent; the reply fields come from the replied-to message, and
// the author is null once the human who wrote it was removed.
func agentMessageJSON(m bus.Message) any {
	out := agentMessageBody{
		ID: m.ID, RoomID: m.RoomID, Text: m.Text, CreatedAt: m.CreatedAt,
		ReplyToMessageID: optionalID(m.ReplyToID),
	}
	if m.Author != nil {
		out.Author = &agentAuthorBody{Kind: m.Author.Kind, ID: m.Author.ID, Name: m.Author.Name, IsBot: m.Author.Kind == bus.ActorAgent}
	}
	if m.ReplyTo != nil {
		text := m.ReplyTo.Text
		out.ReplyToText = &text
		if m.ReplyTo.Author != nil {
			name := m.ReplyTo.Author.Name
			out.ReplyToAuthorName = &name
		}
	}
	return out
}

// activityJSON builds the activity body, resolving the agent that made it.
func (h *controller) activityJSON(ctx context.Context, a bus.Activity) (any, error) {
	agent, err := h.svc.GetAgent(ctx, a.AgentID)
	if err != nil {
		return nil, err
	}
	return activityBody{
		ID: a.ID, MessageID: a.MessageID, Agent: agentJSON(agent), Kind: a.Kind,
		Text: a.Text, Tool: a.Tool, Preview: a.Preview, Arguments: a.Arguments,
		DurationMs: a.DurationMs, Succeeded: a.Succeeded, CreatedAt: a.CreatedAt,
	}, nil
}

// jsonString quotes s as a JSON string literal, for building a frame's data by hand.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// badRequest answers 400 bad_request with message and aborts.
func badRequest(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "bad_request", "message": message}})
}

// readJSON decodes at most limit bytes of body into dst. On any failure it answers
// badRequest "request body must be a JSON object" and returns false.
func readJSON(c *gin.Context, limit int64, dst any) bool {
	r := http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	if err := json.NewDecoder(r).Decode(dst); err != nil {
		badRequest(c, "request body must be a JSON object")
		return false
	}
	return true
}

// parseID parses an ID the caller sent in field; on failure it answers badRequest.
func parseID(c *gin.Context, field, s string) (ulid.ULID, bool) {
	id, err := ulid.ParseStrict(s)
	if err != nil {
		badRequest(c, field+" must be an id")
		return ulid.ULID{}, false
	}
	return id, true
}

// pathID parses the path parameter name. One that is not an ID cannot name anything.
func pathID(c *gin.Context, name string) (ulid.ULID, bool) {
	id, err := ulid.ParseStrict(c.Param(name))
	if err != nil {
		writeError(c, bus.ErrNotFound)
		return ulid.ULID{}, false
	}
	return id, true
}

// bodyID reads a body of the form {field: "<id>"} and returns the ID.
func bodyID(c *gin.Context, field string) (ulid.ULID, bool) {
	var body map[string]any
	if !readJSON(c, maxBody, &body) {
		return ulid.ULID{}, false
	}
	s, _ := body[field].(string)
	return parseID(c, field, s)
}
