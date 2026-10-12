package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// AgentUploadAttachment stores one file the agent uploads to a room. The type
// is sniffed from the first 512 bytes; the request's Content-Type header is
// never read, and a body over the cap is refused before a byte of it is read.
func (h *controller) AgentUploadAttachment(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	if c.Request.ContentLength > bus.MaxAttachmentBytes {
		writeError(c, bus.ErrTooLarge)
		return
	}
	// A transfer gets its own deadline: the server's write timeout counts from
	// the request's headers, which a slow upload outlives. A writer without
	// deadline support (a test recorder) cannot bound the transfer, so there
	// is nothing to bound it against either: skip it.
	rc := http.NewResponseController(c.Writer)
	derr := rc.SetWriteDeadline(time.Now().Add(h.opts.TransferTimeout))
	if derr != nil && !errors.Is(derr, http.ErrNotSupported) {
		writeError(c, fmt.Errorf("upload: set write deadline: %w", derr))
		return
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(c.Request.Body, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		badRequest(c, "could not read the file")
		return
	}
	head = head[:n]
	contentType := http.DetectContentType(head)
	name, _ := c.GetQuery("name")
	att, err := h.svc.UploadAttachment(c.Request.Context(), actorFrom(c), roomID, name, contentType, io.MultiReader(bytes.NewReader(head), c.Request.Body))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, attachmentJSON(att))
}

// GetAttachment serves one attachment to a human member of its room. Only a
// file already on a message can be fetched, and nothing a browser may execute
// inline is served inline: anything that is not a known safe image type is a
// download.
func (h *controller) GetAttachment(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	attachmentID, ok := pathID(c, "attachment")
	if !ok {
		return
	}
	att, file, err := h.svc.OpenAttachment(c.Request.Context(), actorFrom(c), roomID, attachmentID)
	if err != nil {
		writeError(c, err)
		return
	}
	defer func() { _ = file.Close() }()

	rc := http.NewResponseController(c.Writer)
	err = rc.SetWriteDeadline(time.Now().Add(h.opts.TransferTimeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(c, fmt.Errorf("download: set write deadline: %w", err))
		return
	}
	if att.Image() {
		c.Header("Content-Type", att.ContentType)
		c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": att.Name}))
	} else {
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Name}))
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox")
	c.Header("Cache-Control", "private, max-age=86400")
	// The empty name keeps ServeContent from guessing a type; it serves ranges.
	http.ServeContent(c.Writer, c.Request, "", att.CreatedAt, file)
}