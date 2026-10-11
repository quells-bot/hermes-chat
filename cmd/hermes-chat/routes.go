package main

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
)

// routes registers all middleware and routes on e.
func routes(e *gin.Engine, ctrl app.Controller, log *slog.Logger) {
	e.Use(app.RequestID(), app.RequestLogger(log), gin.Recovery())

	// Public: no credentials.
	e.GET("/healthz", ctrl.Healthz)
	e.POST("/api/login", app.SameOrigin(), ctrl.Login)

	// Human API: session cookie, same-origin for state changes.
	human := e.Group("/api", app.SameOrigin(), ctrl.HumanAuth)
	human.POST("/logout", ctrl.Logout)
	human.GET("/me", ctrl.Me)

	// Agent API: bearer token.
	agent := e.Group("/agent", ctrl.AgentAuth)
	agent.GET("/me", ctrl.AgentMe)
}
