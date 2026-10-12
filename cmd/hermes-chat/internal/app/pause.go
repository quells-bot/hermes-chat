package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// PauseRoom pauses the room; pausing a paused room still succeeds.
func (h *controller) PauseRoom(c *gin.Context) {
	roomAction(c, func(roomID ulid.ULID) (bus.Room, error) {
		return h.svc.PauseRoom(c.Request.Context(), actorFrom(c), roomID)
	})
}

// ResumeRoom lifts a room's pause, whatever its reason.
func (h *controller) ResumeRoom(c *gin.Context) {
	roomAction(c, func(roomID ulid.ULID) (bus.Room, error) {
		return h.svc.ResumeRoom(c.Request.Context(), actorFrom(c), roomID)
	})
}

// haltReply answers with whether everything is stopped.
func (h *controller) haltReply(c *gin.Context) {
	halted, err := h.svc.Halted(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"halted": halted})
}

// GetHalt reports whether everything is stopped.
func (h *controller) GetHalt(c *gin.Context) { h.haltReply(c) }

// StopEverything stops every room at once.
func (h *controller) StopEverything(c *gin.Context) {
	if err := h.svc.StopEverything(c.Request.Context(), actorFrom(c)); err != nil {
		writeError(c, err)
		return
	}
	h.haltReply(c)
}

// ResumeEverything lifts stop-everything; each room keeps its own pause.
func (h *controller) ResumeEverything(c *gin.Context) {
	if err := h.svc.ResumeEverything(c.Request.Context(), actorFrom(c)); err != nil {
		writeError(c, err)
		return
	}
	h.haltReply(c)
}
