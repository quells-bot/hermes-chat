package app

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// AgentPostPrompt posts the agent's question as a prompt message.
func (h *controller) AgentPostPrompt(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	var body struct {
		Kind    string   `json:"kind"`
		Text    string   `json:"text"`
		Choices []string `json:"choices"`
	}
	if !readJSON(c, maxMessageBody, &body) {
		return
	}
	msg, err := h.svc.PostPrompt(c.Request.Context(), actorFrom(c), roomID, bus.PromptKind(body.Kind), body.Text, body.Choices)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, messageJSON(msg))
}

// AgentExpirePrompt closes a prompt the asking agent gave up on.
func (h *controller) AgentExpirePrompt(c *gin.Context) {
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	if err := h.svc.ExpirePrompt(c.Request.Context(), actorFrom(c), messageID); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AnswerPrompt answers a question an agent asked the room.
func (h *controller) AnswerPrompt(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	var body struct {
		Answer string `json:"answer"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	msg, err := h.svc.AnswerPrompt(c.Request.Context(), actorFrom(c), roomID, messageID, body.Answer)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, messageJSON(msg))
}