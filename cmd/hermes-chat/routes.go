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
	human.GET("/agents", ctrl.ListAgents)
	human.GET("/humans", ctrl.ListHumans)
	human.GET("/rooms", ctrl.ListRooms)
	human.POST("/rooms", ctrl.CreateRoom)
	human.POST("/dms", ctrl.OpenDM)
	human.GET("/rooms/:room", ctrl.GetRoom)
	human.PATCH("/rooms/:room", ctrl.UpdateRoom)
	human.POST("/rooms/:room/humans", ctrl.InviteHuman)
	human.POST("/rooms/:room/agents", ctrl.AddRoomAgent)
	human.DELETE("/rooms/:room/agents/:agent", ctrl.RemoveRoomAgent)
	human.PUT("/rooms/:room/read", ctrl.MarkRoomRead)
	human.GET("/rooms/:room/messages", ctrl.ListMessages)
	human.POST("/rooms/:room/messages", ctrl.PostMessage)
	human.GET("/rooms/:room/messages/:message", ctrl.GetMessage)
	human.GET("/events", ctrl.Events)

	// Agent API: bearer token.
	agent := e.Group("/agent", ctrl.AgentAuth)
	agent.GET("/me", ctrl.AgentMe)
}
