package app

import (
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

type roomBody struct {
	ID                ulid.ULID   `json:"id"`
	Flavor            string      `json:"flavor"`
	Topic             string      `json:"topic"`
	Archived          bool        `json:"archived"`
	CreatedAt         time.Time   `json:"created_at"`
	Humans            []humanBody `json:"humans"`
	Agents            []agentBody `json:"agents"`
	LastMessageID     *ulid.ULID  `json:"last_message_id"`
	LastReadMessageID *ulid.ULID  `json:"last_read_message_id"`
}

type authorBody struct {
	Kind bus.ActorKind `json:"kind"`
	ID   ulid.ULID     `json:"id"`
	Name string        `json:"name"`
}

type messageBody struct {
	ID        ulid.ULID   `json:"id"`
	RoomID    ulid.ULID   `json:"room_id"`
	Author    *authorBody `json:"author"`
	Text      string      `json:"text"`
	ReplyToID *ulid.ULID  `json:"reply_to_id"`
	CreatedAt time.Time   `json:"created_at"`
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
		Humans: make([]humanBody, 0, len(r.Humans)), Agents: make([]agentBody, 0, len(r.Agents)),
		LastMessageID: optionalID(r.LastMessageID), LastReadMessageID: optionalID(r.LastReadMessageID),
	}
	for _, h := range r.Humans {
		out.Humans = append(out.Humans, humanJSON(h))
	}
	for _, ra := range r.Agents {
		out.Agents = append(out.Agents, agentJSON(ra.Agent))
	}
	return out
}

// messageJSON is the message object; author is null once its human was removed.
func messageJSON(m bus.Message) any {
	out := messageBody{ID: m.ID, RoomID: m.RoomID, Text: m.Text, ReplyToID: optionalID(m.ReplyToID), CreatedAt: m.CreatedAt}
	if m.Author != nil {
		out.Author = &authorBody{Kind: m.Author.Kind, ID: m.Author.ID, Name: m.Author.Name}
	}
	return out
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
