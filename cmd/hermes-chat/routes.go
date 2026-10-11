package main

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
)

// routes registers all middleware and routes on e.
func routes(e *gin.Engine, ctrl app.Controller, log *slog.Logger) {
	e.Use(app.RequestID(), app.RequestLogger(log), gin.Recovery())
	e.GET("/healthz", ctrl.Healthz)
}
