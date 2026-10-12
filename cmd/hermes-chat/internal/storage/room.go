package storage

import (
	"context"
	"database/sql"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Room is a topic room or a direct-message room between one human and one agent.
type Room struct {
	ID, Topic            string  // ULID text; Topic is "" for a DM
	DMHumanID, DMAgentID *string // both set for a DM, both nil otherwise
	CreatedAtMs          int64   // Unix milliseconds, UTC
	ArchivedAtMs         *int64  // nil while live
}

// RoomHuman is a human's membership in a room.
type RoomHuman struct {
	RoomID, HumanID   string
	JoinedAtMs        int64  // Unix milliseconds, UTC
	LastReadMessageID string // "" when nothing is read
}

// RoomAgent is an agent's membership in a room.
type RoomAgent struct {
	RoomID, AgentID string
	JoinedAtMs      int64 // Unix milliseconds, UTC
}

func nullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func stringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func roomFromRow(row sqlcgen.Room) Room {
	rm := Room{
		ID: row.ID, Topic: row.Topic,
		DMHumanID: stringPtr(row.DmHumanID), DMAgentID: stringPtr(row.DmAgentID),
		CreatedAtMs: row.CreatedAt,
	}
	if row.ArchivedAt.Valid {
		v := row.ArchivedAt.Int64
		rm.ArchivedAtMs = &v
	}
	return rm
}

func (r *repo) PutRoom(ctx context.Context, rm Room) error {
	return mapErr(r.writes.InsertRoom(ctx, sqlcgen.InsertRoomParams{
		ID: rm.ID, Topic: rm.Topic,
		DmHumanID: nullString(rm.DMHumanID), DmAgentID: nullString(rm.DMAgentID),
		CreatedAt: rm.CreatedAtMs, ArchivedAt: nullInt64(rm.ArchivedAtMs),
	}))
}

func (r *repo) GetRoom(ctx context.Context, id string) (Room, error) {
	row, err := r.reads.GetRoom(ctx, id)
	if err != nil {
		return Room{}, mapErr(err)
	}
	return roomFromRow(row), nil
}

func (r *repo) GetDMRoom(ctx context.Context, humanID, agentID string) (Room, error) {
	row, err := r.reads.GetDMRoom(ctx, sqlcgen.GetDMRoomParams{
		DmHumanID: nullString(&humanID), DmAgentID: nullString(&agentID),
	})
	if err != nil {
		return Room{}, mapErr(err)
	}
	return roomFromRow(row), nil
}

func (r *repo) ListRoomsByHuman(ctx context.Context, humanID string) ([]Room, error) {
	rows, err := r.reads.ListRoomsByHuman(ctx, humanID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Room, 0, len(rows))
	for _, row := range rows {
		out = append(out, roomFromRow(row))
	}
	return out, nil
}

func (r *repo) SetRoomTopic(ctx context.Context, id, topic string) error {
	return requireRow(r.writes.SetRoomTopic(ctx, sqlcgen.SetRoomTopicParams{Topic: topic, ID: id}))
}

func (r *repo) SetRoomArchivedAt(ctx context.Context, id string, archivedAtMs *int64) error {
	return requireRow(r.writes.SetRoomArchivedAt(ctx, sqlcgen.SetRoomArchivedAtParams{ArchivedAt: nullInt64(archivedAtMs), ID: id}))
}

func (r *repo) PutRoomHuman(ctx context.Context, m RoomHuman) error {
	return mapErr(r.writes.InsertRoomHuman(ctx, sqlcgen.InsertRoomHumanParams{
		RoomID: m.RoomID, HumanID: m.HumanID, JoinedAt: m.JoinedAtMs, LastReadMessageID: m.LastReadMessageID,
	}))
}

func (r *repo) GetRoomHuman(ctx context.Context, roomID, humanID string) (RoomHuman, error) {
	row, err := r.reads.GetRoomHuman(ctx, sqlcgen.GetRoomHumanParams{RoomID: roomID, HumanID: humanID})
	if err != nil {
		return RoomHuman{}, mapErr(err)
	}
	return RoomHuman{
		RoomID: row.RoomID, HumanID: row.HumanID, JoinedAtMs: row.JoinedAt, LastReadMessageID: row.LastReadMessageID,
	}, nil
}

func (r *repo) ListRoomHumans(ctx context.Context, roomID string) ([]Human, error) {
	rows, err := r.reads.ListRoomHumans(ctx, roomID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Human, 0, len(rows))
	for _, row := range rows {
		out = append(out, Human{ID: row.ID, Username: row.Username, CreatedAtMs: row.CreatedAt})
	}
	return out, nil
}

func (r *repo) SetRoomHumanLastRead(ctx context.Context, roomID, humanID, messageID string) error {
	return requireRow(r.writes.SetRoomHumanLastRead(ctx, sqlcgen.SetRoomHumanLastReadParams{
		LastReadMessageID: messageID, RoomID: roomID, HumanID: humanID,
	}))
}

func (r *repo) PutRoomAgent(ctx context.Context, m RoomAgent) error {
	return mapErr(r.writes.InsertRoomAgent(ctx, sqlcgen.InsertRoomAgentParams{
		RoomID: m.RoomID, AgentID: m.AgentID, JoinedAt: m.JoinedAtMs,
	}))
}

func (r *repo) DeleteRoomAgent(ctx context.Context, roomID, agentID string) error {
	return requireRow(r.writes.DeleteRoomAgent(ctx, sqlcgen.DeleteRoomAgentParams{RoomID: roomID, AgentID: agentID}))
}

func (r *repo) ListRoomAgents(ctx context.Context, roomID string) ([]Agent, error) {
	rows, err := r.reads.ListRoomAgents(ctx, roomID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentFromRow(row))
	}
	return out, nil
}
