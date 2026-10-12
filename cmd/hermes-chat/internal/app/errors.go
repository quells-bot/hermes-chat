package app

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// errorRow maps one bus sentinel to its HTTP response.
type errorRow struct {
	target  error
	status  int
	code    string
	message string
}

var errorTable = []errorRow{
	{bus.ErrValidation, http.StatusBadRequest, "validation", ""}, // message comes from the ValidationError
	{bus.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials", "wrong username or password"},
	{bus.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated", "authentication required"},
	{bus.ErrForbidden, http.StatusForbidden, "forbidden", "not allowed"},
	{bus.ErrNotFound, http.StatusNotFound, "not_found", "not found"},
	{bus.ErrConflict, http.StatusConflict, "conflict", "already exists"},
	{bus.ErrRoomArchived, http.StatusConflict, "room_archived", "room is archived"},
	{bus.ErrNoTurn, http.StatusConflict, "no_turn", "no turn is running"},
	{bus.ErrNothingWaiting, http.StatusConflict, "nothing_waiting", "no waiting delivery for this message"},
	{bus.ErrUnavailable, http.StatusServiceUnavailable, "unavailable", "shutting down; try again shortly"},
	{bus.ErrThrottled, http.StatusTooManyRequests, "throttled", "too many attempts; try again later"},
}

// writeError is the one place bus errors become HTTP responses. It never sends
// a bus error's text to the client except a ValidationError's message, and it
// aborts the chain.
func writeError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "internal", "internal error"
	for _, row := range errorTable {
		if !errors.Is(err, row.target) {
			continue
		}
		status, code, message = row.status, row.code, row.message
		if row.target == bus.ErrValidation {
			var ve bus.ValidationError
			if errors.As(err, &ve) {
				message = ve.Message
			} else {
				message = "invalid request"
			}
		}
		if row.target == bus.ErrThrottled {
			var te bus.ThrottledError
			secs := 1
			if errors.As(err, &te) {
				secs = max(1, int(math.Ceil(te.RetryAfter.Seconds())))
			}
			c.Header("Retry-After", strconv.Itoa(secs))
		}
		break
	}
	if status == http.StatusInternalServerError {
		slog.ErrorContext(c.Request.Context(), "request failed", "error", err)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
