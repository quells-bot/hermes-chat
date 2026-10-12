package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// AgentRooms lists the agent's rooms, ordered by id.
func (h *controller) AgentRooms(c *gin.Context) {
	rooms, err := h.svc.ListRooms(c.Request.Context(), actorFrom(c))
	if err != nil {
		writeError(c, err)
		return
	}
	out := make([]any, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, agentRoomJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"rooms": out})
}

// AgentPostMessage posts a message into a room the agent belongs to. The
// response is the human message shape.
func (h *controller) AgentPostMessage(c *gin.Context) {
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

// AgentEditMessage rewrites the body of one of the agent's own messages.
func (h *controller) AgentEditMessage(c *gin.Context) {
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	var body struct {
		Text  string `json:"text"`
		Final bool   `json:"final"`
	}
	if !readJSON(c, maxMessageBody, &body) {
		return
	}
	if err := h.svc.EditMessage(c.Request.Context(), actorFrom(c), messageID, body.Text, body.Final); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// turnReport runs one of the agent's turn reports on its delivery of the
// delivered message. The turn reports take no body.
func turnReport(c *gin.Context, do func(messageID ulid.ULID) error) {
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	if err := do(messageID); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AgentTurnWorking marks the agent's delivery of the delivered message working.
func (h *controller) AgentTurnWorking(c *gin.Context) {
	turnReport(c, func(messageID ulid.ULID) error {
		return h.svc.TurnWorking(c.Request.Context(), actorFrom(c), messageID)
	})
}

// AgentTurnDone reports the turn done.
func (h *controller) AgentTurnDone(c *gin.Context) {
	turnReport(c, func(messageID ulid.ULID) error {
		return h.svc.TurnDone(c.Request.Context(), actorFrom(c), messageID)
	})
}

// AgentTurnFailed reports the turn failed.
func (h *controller) AgentTurnFailed(c *gin.Context) {
	turnReport(c, func(messageID ulid.ULID) error {
		return h.svc.TurnFailed(c.Request.Context(), actorFrom(c), messageID)
	})
}

// AgentRemark records a remark the agent made while working on a delivered message.
func (h *controller) AgentRemark(c *gin.Context) {
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(c, maxMessageBody, &body) {
		return
	}
	act, err := h.svc.ReportRemark(c.Request.Context(), actorFrom(c), messageID, body.Text)
	if err != nil {
		writeError(c, err)
		return
	}
	h.answerActivity(c, http.StatusCreated, act)
}

// AgentStartTool records a tool call the agent started while working on a
// delivered message.
func (h *controller) AgentStartTool(c *gin.Context) {
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	var body struct {
		Tool      string `json:"tool"`
		Preview   string `json:"preview"`
		Arguments string `json:"arguments"`
	}
	if !readJSON(c, maxMessageBody, &body) {
		return
	}
	act, err := h.svc.StartTool(c.Request.Context(), actorFrom(c), messageID, body.Tool, body.Preview, body.Arguments)
	if err != nil {
		writeError(c, err)
		return
	}
	h.answerActivity(c, http.StatusCreated, act)
}

// AgentFinishTool records a tool call's result.
func (h *controller) AgentFinishTool(c *gin.Context) {
	activityID, ok := pathID(c, "activity")
	if !ok {
		return
	}
	var body struct {
		DurationMs int64 `json:"duration_ms"`
		Succeeded  bool  `json:"succeeded"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	act, err := h.svc.FinishTool(c.Request.Context(), actorFrom(c), activityID, body.DurationMs, body.Succeeded)
	if err != nil {
		writeError(c, err)
		return
	}
	h.answerActivity(c, http.StatusOK, act)
}

// answerActivity replies with the activity body, resolving the agent that made it.
func (h *controller) answerActivity(c *gin.Context, status int, act bus.Activity) {
	body, err := h.activityJSON(c.Request.Context(), act)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(status, body)
}
