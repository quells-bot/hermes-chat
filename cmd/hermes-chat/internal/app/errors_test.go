package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func TestWriteError_Table(t *testing.T) {
	cases := []struct {
		err    error
		status int
		body   string
	}{
		{bus.ValidationError{Message: "username must be nicer"}, 400, `{"error":{"code":"validation","message":"username must be nicer"}}`},
		{fmt.Errorf("login: %w", bus.ErrInvalidCredentials), 401, `{"error":{"code":"invalid_credentials","message":"wrong username or password"}}`},
		{bus.ErrUnauthenticated, 401, `{"error":{"code":"unauthenticated","message":"authentication required"}}`},
		{bus.ErrForbidden, 403, `{"error":{"code":"forbidden","message":"not allowed"}}`},
		{fmt.Errorf("agent %q: %w", "secret-handle", bus.ErrNotFound), 404, `{"error":{"code":"not_found","message":"not found"}}`},
		{fmt.Errorf("room: %w", bus.ErrPaused), 409, `{"error":{"code":"paused","message":"the room is paused or everything is stopped"}}`},
		{fmt.Errorf("x: %w", bus.ErrPromptClosed), 409, `{"error":{"code":"prompt_closed","message":"the prompt is no longer open"}}`},
		{fmt.Errorf("username %q is already taken: %w", "kai", bus.ErrConflict), 409, `{"error":{"code":"conflict","message":"already exists"}}`},
		{bus.ThrottledError{RetryAfter: 1500 * time.Millisecond}, 429, `{"error":{"code":"throttled","message":"too many attempts; try again later"}}`},
		{bus.ErrRoomArchived, 409, `{"error":{"code":"room_archived","message":"room is archived"}}`},
		{fmt.Errorf("subscribe: %w", bus.ErrUnavailable), 503, `{"error":{"code":"unavailable","message":"shutting down; try again shortly"}}`},
		{errors.New("disk on fire"), 500, `{"error":{"code":"internal","message":"internal error"}}`},
		{fmt.Errorf("x: %w", storage.ErrNotFound), 500, `{"error":{"code":"internal","message":"internal error"}}`}, // a storage sentinel that leaked is a bug, not a 404
	}
	for _, tc := range cases {
		e, buf := newEngine(t, realSvc(t))
		e.GET("/fail", func(c *gin.Context) { app.WriteError(c, tc.err) })
		w := do(e, "GET", "/fail")
		assert.Equal(t, tc.status, w.Code, tc.err)
		assert.JSONEq(t, tc.body, w.Body.String(), tc.err)
		logged := strings.Contains(buf.String(), tc.err.Error())
		assert.Equal(t, tc.status == 500, logged, "only a 500 logs the real error: %v", tc.err)
		if tc.status == 429 {
			assert.Equal(t, "2", w.Header().Get("Retry-After"))
		} else {
			assert.Empty(t, w.Header().Get("Retry-After"))
		}
	}
}

func TestWriteError_RetryAfterIsAtLeastOne(t *testing.T) {
	e, _ := newEngine(t, realSvc(t))
	e.GET("/fail", func(c *gin.Context) { app.WriteError(c, bus.ThrottledError{RetryAfter: time.Millisecond}) })
	assert.Equal(t, "1", do(e, "GET", "/fail").Header().Get("Retry-After"))
}
