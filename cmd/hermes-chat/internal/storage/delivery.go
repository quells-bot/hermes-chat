package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Delivery is one agent's copy of a message in a room, waiting in the
// agent's queue or settled.
type Delivery struct {
	RoomID, AgentID, MessageID string
	State                      string // one of the six bus state names; the table CHECKs them
}

func deliveryFromRow(row sqlcgen.Delivery) Delivery {
	return Delivery{RoomID: row.RoomID, AgentID: row.AgentID, MessageID: row.MessageID, State: row.State}
}

func deliveriesFromRows(rows []sqlcgen.Delivery) []Delivery {
	out := make([]Delivery, 0, len(rows))
	for _, row := range rows {
		out = append(out, deliveryFromRow(row))
	}
	return out
}

func (r *repo) PutDelivery(ctx context.Context, d Delivery) error {
	return mapErr(r.writes.InsertDelivery(ctx, sqlcgen.InsertDeliveryParams{
		RoomID: d.RoomID, AgentID: d.AgentID, MessageID: d.MessageID, State: d.State,
	}))
}

func (r *repo) GetDelivery(ctx context.Context, agentID, messageID string) (Delivery, error) {
	row, err := r.reads.GetDelivery(ctx, sqlcgen.GetDeliveryParams{AgentID: agentID, MessageID: messageID})
	if err != nil {
		return Delivery{}, mapErr(err)
	}
	return deliveryFromRow(row), nil
}

func (r *repo) GetWorkingDelivery(ctx context.Context, roomID, agentID string) (Delivery, error) {
	row, err := r.reads.GetWorkingDelivery(ctx, sqlcgen.GetWorkingDeliveryParams{RoomID: roomID, AgentID: agentID})
	if err != nil {
		return Delivery{}, mapErr(err)
	}
	return deliveryFromRow(row), nil
}

func (r *repo) GetDeliveredDelivery(ctx context.Context, roomID, agentID string) (Delivery, error) {
	row, err := r.reads.GetDeliveredDelivery(ctx, sqlcgen.GetDeliveredDeliveryParams{RoomID: roomID, AgentID: agentID})
	if err != nil {
		return Delivery{}, mapErr(err)
	}
	return deliveryFromRow(row), nil
}

func (r *repo) ListDeliveriesForMessage(ctx context.Context, roomID, messageID string) ([]Delivery, error) {
	rows, err := r.reads.ListDeliveriesForMessage(ctx, sqlcgen.ListDeliveriesForMessageParams{RoomID: roomID, MessageID: messageID})
	if err != nil {
		return nil, mapErr(err)
	}
	return deliveriesFromRows(rows), nil
}

func (r *repo) ListDeliveriesInRange(ctx context.Context, roomID, firstID, lastID string) ([]Delivery, error) {
	rows, err := r.reads.ListDeliveriesInRange(ctx, sqlcgen.ListDeliveriesInRangeParams{RoomID: roomID, MessageID: firstID, MessageID_2: lastID})
	if err != nil {
		return nil, mapErr(err)
	}
	return deliveriesFromRows(rows), nil
}

func (r *repo) HeadWaiting(ctx context.Context, roomID, agentID string) (Delivery, bool, error) {
	row, err := r.reads.HeadWaiting(ctx, sqlcgen.HeadWaitingParams{RoomID: roomID, AgentID: agentID})
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, mapErr(err)
	}
	return deliveryFromRow(row), true, nil
}

func (r *repo) ReleasableDeliveries(ctx context.Context, agentID string) ([]Delivery, error) {
	rows, err := r.reads.ReleasableDeliveries(ctx, agentID)
	if err != nil {
		return nil, mapErr(err)
	}
	return deliveriesFromRows(rows), nil
}

func (r *repo) SetDeliveryState(ctx context.Context, roomID, agentID, messageID, state string) error {
	return requireRow(r.writes.SetDeliveryState(ctx, sqlcgen.SetDeliveryStateParams{
		State: state, RoomID: roomID, AgentID: agentID, MessageID: messageID,
	}))
}

func (r *repo) CancelDeliveries(ctx context.Context, roomID, agentID string) (int64, error) {
	return r.writes.CancelDeliveries(ctx, sqlcgen.CancelDeliveriesParams{RoomID: roomID, AgentID: agentID})
}

func (r *repo) ListAgentRoomIDs(ctx context.Context, agentID string) ([]string, error) {
	rows, err := r.reads.ListAgentRoomIDs(ctx, agentID)
	if err != nil {
		return nil, mapErr(err)
	}
	if rows == nil {
		return []string{}, nil
	}
	return rows, nil
}
