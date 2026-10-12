package main

import (
	"io"
	"log/slog"

	"github.com/quells-bot/hermes-chat/internal/reqid"
)

// newLogger returns a JSON logger that adds the request ID found in the log context.
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(reqid.NewHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})))
}
