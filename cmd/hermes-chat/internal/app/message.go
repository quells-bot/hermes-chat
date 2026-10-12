package app

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// maxMessageBody bounds a posted message's body: the text cap, with room for JSON escaping.
const maxMessageBody = 1 << 20

// MarkRoomRead moves the caller's read marker forward.
func (h *controller) MarkRoomRead(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	messageID, ok := bodyID(c, "message_id")
	if !ok {
		return
	}
	if err := h.svc.MarkRoomRead(c.Request.Context(), actorFrom(c), roomID, messageID); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMessages pages through a room's messages, oldest first.
func (h *controller) ListMessages(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	limit := 0 // the bus default
	if s, present := c.GetQuery("limit"); present {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > bus.MaxPageSize {
			badRequest(c, "limit must be an integer from 1 to 200")
			return
		}
		limit = n
	}
	var before ulid.ULID
	if s, present := c.GetQuery("before"); present {
		if before, ok = parseID(c, "before", s); !ok {
			return
		}
	}
	msgs, err := h.svc.ListMessages(c.Request.Context(), actorFrom(c), roomID, before, limit)
	if err != nil {
		writeError(c, err)
		return
	}
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageJSON(m))
	}
	c.JSON(http.StatusOK, gin.H{"messages": out})
}

// PostMessage adds a message to a room.
func (h *controller) PostMessage(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	var body struct {
		Text      string  `json:"text"`
		ReplyToID *string `json:"reply_to_id"`
	}
	if !readJSON(c, maxMessageBody, &body) {
		return
	}
	var replyTo ulid.ULID
	if body.ReplyToID != nil {
		if replyTo, ok = parseID(c, "reply_to_id", *body.ReplyToID); !ok {
			return
		}
	}
	msg, err := h.svc.PostMessage(c.Request.Context(), actorFrom(c), roomID, body.Text, replyTo)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, messageJSON(msg))
}

// GetMessage describes one message in a room the caller belongs to.
func (h *controller) GetMessage(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	msg, err := h.svc.GetMessage(c.Request.Context(), actorFrom(c), roomID, messageID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, messageJSON(msg))
}
