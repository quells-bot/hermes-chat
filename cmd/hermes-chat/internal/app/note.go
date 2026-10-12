package app

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// GetNote returns the caller's own note.
func (h *controller) GetNote(c *gin.Context) {
	n, err := h.svc.GetNote(c.Request.Context(), actorFrom(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, noteJSON(n))
}

// SetNoteText replaces the caller's note text.
func (h *controller) SetNoteText(c *gin.Context) {
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	n, err := h.svc.SetNoteText(c.Request.Context(), actorFrom(c), body.Text)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, noteJSON(n))
}

// UpdateNoteEntry changes one of the caller's own entries: its text, whether
// it is approved, or both.
func (h *controller) UpdateNoteEntry(c *gin.Context) {
	entryID, ok := pathID(c, "entry")
	if !ok {
		return
	}
	var body struct {
		Text     *string `json:"text"`
		Approved *bool   `json:"approved"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	n, err := h.svc.UpdateNoteEntry(c.Request.Context(), actorFrom(c), entryID, bus.NoteEntryUpdate{Text: body.Text, Approved: body.Approved})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, noteJSON(n))
}

// DeleteNoteEntry removes one of the caller's own entries.
func (h *controller) DeleteNoteEntry(c *gin.Context) {
	entryID, ok := pathID(c, "entry")
	if !ok {
		return
	}
	n, err := h.svc.DeleteNoteEntry(c.Request.Context(), actorFrom(c), entryID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, noteJSON(n))
}

// AgentSuggestNoteEntry quarantines an entry the agent suggests for the note
// of the human who wrote the delivered message. Suggesting the same text
// again returns the first entry with 200.
func (h *controller) AgentSuggestNoteEntry(c *gin.Context) {
	messageID, ok := pathID(c, "message")
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	e, created, err := h.svc.SuggestNoteEntry(c.Request.Context(), actorFrom(c), messageID, body.Text)
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, noteEntryJSON(e))
}