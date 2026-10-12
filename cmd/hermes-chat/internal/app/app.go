// Package app is the HTTP layer: gin handlers, middleware, and JSON.
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// RequestIDHeader is the response header carrying the request ID.
const RequestIDHeader = "X-Request-ID"

// Controller is the set of HTTP handlers.
type Controller interface {
	Healthz(c *gin.Context)
	HumanAuth(c *gin.Context)
	AgentAuth(c *gin.Context)
	Login(c *gin.Context)
	Logout(c *gin.Context)
	Me(c *gin.Context)
	AgentMe(c *gin.Context)
	ListAgents(c *gin.Context)
	ListHumans(c *gin.Context)
	ListRooms(c *gin.Context)
	CreateRoom(c *gin.Context)
	OpenDM(c *gin.Context)
	GetRoom(c *gin.Context)
	UpdateRoom(c *gin.Context)
	InviteHuman(c *gin.Context)
	AddRoomAgent(c *gin.Context)
	RemoveRoomAgent(c *gin.Context)
	MarkRoomRead(c *gin.Context)
	ListMessages(c *gin.Context)
	PostMessage(c *gin.Context)
	GetMessage(c *gin.Context)
	CancelMessage(c *gin.Context)
	ListActivity(c *gin.Context)
	StopTurn(c *gin.Context)
	Steer(c *gin.Context)
	PauseRoom(c *gin.Context)
	ResumeRoom(c *gin.Context)
	GetHalt(c *gin.Context)
	StopEverything(c *gin.Context)
	ResumeEverything(c *gin.Context)
	Events(c *gin.Context)
	AgentEvents(c *gin.Context)
	AgentRooms(c *gin.Context)
	AgentPostMessage(c *gin.Context)
	AgentEditMessage(c *gin.Context)
	AgentTurnWorking(c *gin.Context)
	AgentTurnDone(c *gin.Context)
	AgentTurnFailed(c *gin.Context)
	AgentRemark(c *gin.Context)
	AgentStartTool(c *gin.Context)
	AgentFinishTool(c *gin.Context)
	AgentPostPrompt(c *gin.Context)
	AgentExpirePrompt(c *gin.Context)
	AnswerPrompt(c *gin.Context)
	GetNote(c *gin.Context)
	SetNoteText(c *gin.Context)
	UpdateNoteEntry(c *gin.Context)
	DeleteNoteEntry(c *gin.Context)
	AgentSuggestNoteEntry(c *gin.Context)
	AgentUploadAttachment(c *gin.Context)
	GetAttachment(c *gin.Context)
}

// Options are the settings the handlers need.
type Options struct {
	CookieSecure       bool          // the Secure attribute on the session cookie
	HeartbeatInterval  time.Duration // between a stream's heartbeats; zero means DefaultHeartbeatInterval
	StreamWriteTimeout time.Duration // for one write to a stream; zero means DefaultStreamWriteTimeout
	TransferTimeout    time.Duration // for an upload or download; zero means DefaultTransferTimeout
}

// DefaultTransferTimeout is how long one file transfer may take: the server's
// write deadline counts from the request's headers, which a slow upload or
// download outlives.
const DefaultTransferTimeout = 10 * time.Minute

type controller struct {
	svc  bus.Service
	opts Options
}

// NewController returns a Controller backed by svc.
func NewController(svc bus.Service, opts Options) *controller {
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if opts.StreamWriteTimeout <= 0 {
		opts.StreamWriteTimeout = DefaultStreamWriteTimeout
	}
	if opts.TransferTimeout <= 0 {
		opts.TransferTimeout = DefaultTransferTimeout
	}
	return &controller{svc: svc, opts: opts}
}

// Healthz reports whether the service can reach its storage.
func (h *controller) Healthz(c *gin.Context) {
	if err := h.svc.Health(c.Request.Context()); err != nil {
		slog.ErrorContext(c.Request.Context(), "health check failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
