package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// maxBody bounds every request body except a message's.
const maxBody = 4096

// ListAgents lists every agent, by handle.
func (h *controller) ListAgents(c *gin.Context) {
	agents, err := h.svc.ListAgents(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	out := make([]agentBody, 0, len(agents))
	for _, a := range agents {
		out = append(out, agentJSON(a))
	}
	c.JSON(http.StatusOK, gin.H{"agents": out})
}

// ListHumans lists every human, by username.
func (h *controller) ListHumans(c *gin.Context) {
	humans, err := h.svc.ListHumans(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	out := make([]humanBody, 0, len(humans))
	for _, u := range humans {
		out = append(out, humanJSON(u))
	}
	c.JSON(http.StatusOK, gin.H{"humans": out})
}

// ListRooms lists the caller's rooms.
func (h *controller) ListRooms(c *gin.Context) {
	rooms, err := h.svc.ListRooms(c.Request.Context(), actorFrom(c))
	if err != nil {
		writeError(c, err)
		return
	}
	out := make([]any, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, roomJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"rooms": out})
}

// CreateRoom makes a topic room.
func (h *controller) CreateRoom(c *gin.Context) {
	var body struct {
		Topic    string   `json:"topic"`
		AgentIDs []string `json:"agent_ids"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	ids := make([]ulid.ULID, 0, len(body.AgentIDs))
	for _, s := range body.AgentIDs {
		id, err := ulid.ParseStrict(s)
		if err != nil {
			badRequest(c, "agent_ids must be a list of ids")
			return
		}
		ids = append(ids, id)
	}
	room, err := h.svc.CreateRoom(c.Request.Context(), actorFrom(c), body.Topic, ids)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, roomJSON(room))
}

// OpenDM returns the caller's DM with an agent, making it if needed.
func (h *controller) OpenDM(c *gin.Context) {
	agentID, ok := bodyID(c, "agent_id")
	if !ok {
		return
	}
	room, created, err := h.svc.OpenDM(c.Request.Context(), actorFrom(c), agentID)
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, roomJSON(room))
}

// roomAction runs a bus call on the room named in the path and answers 200 with the room.
func roomAction(c *gin.Context, do func(roomID ulid.ULID) (bus.Room, error)) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	room, err := do(roomID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, roomJSON(room))
}

// roomActionWithID is roomAction for calls that also take an ID from the body field.
func roomActionWithID(c *gin.Context, field string, do func(roomID, id ulid.ULID) (bus.Room, error)) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	id, ok := bodyID(c, field)
	if !ok {
		return
	}
	room, err := do(roomID, id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, roomJSON(room))
}

// GetRoom describes a room the caller belongs to.
func (h *controller) GetRoom(c *gin.Context) {
	roomAction(c, func(roomID ulid.ULID) (bus.Room, error) {
		return h.svc.GetRoom(c.Request.Context(), actorFrom(c), roomID)
	})
}

// UpdateRoom changes a room's topic or archived state; absent or null fields are left alone.
func (h *controller) UpdateRoom(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	var body struct {
		Topic    *string `json:"topic"`
		Archived *bool   `json:"archived"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	room, err := h.svc.UpdateRoom(c.Request.Context(), actorFrom(c), roomID, bus.RoomUpdate{Topic: body.Topic, Archived: body.Archived})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, roomJSON(room))
}

// InviteHuman makes another human a member of the room.
func (h *controller) InviteHuman(c *gin.Context) {
	roomActionWithID(c, "human_id", func(roomID, humanID ulid.ULID) (bus.Room, error) {
		return h.svc.InviteHuman(c.Request.Context(), actorFrom(c), roomID, humanID)
	})
}

// AddRoomAgent adds an agent to the room.
func (h *controller) AddRoomAgent(c *gin.Context) {
	roomActionWithID(c, "agent_id", func(roomID, agentID ulid.ULID) (bus.Room, error) {
		return h.svc.AddRoomAgent(c.Request.Context(), actorFrom(c), roomID, agentID)
	})
}

// RemoveRoomAgent takes an agent out of the room.
func (h *controller) RemoveRoomAgent(c *gin.Context) {
	agentID, ok := pathID(c, "agent")
	if !ok {
		return
	}
	roomAction(c, func(roomID ulid.ULID) (bus.Room, error) {
		return h.svc.RemoveRoomAgent(c.Request.Context(), actorFrom(c), roomID, agentID)
	})
}

// StopTurn asks an agent to stop its running turn; a room with no turn still succeeds.
func (h *controller) StopTurn(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	agentID, ok := pathID(c, "agent")
	if !ok {
		return
	}
	if err := h.svc.StopTurn(c.Request.Context(), actorFrom(c), roomID, agentID); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Steer redirects an agent's running turn with a new instruction.
func (h *controller) Steer(c *gin.Context) {
	roomID, ok := pathID(c, "room")
	if !ok {
		return
	}
	agentID, ok := pathID(c, "agent")
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(c, maxBody, &body) {
		return
	}
	msg, err := h.svc.Steer(c.Request.Context(), actorFrom(c), roomID, agentID, body.Text)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, messageJSON(msg))
}
