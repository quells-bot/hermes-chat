package storage

import (
	"context"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Human is a registered user.
type Human struct {
	ID          string // ULID text
	Username    string
	CreatedAtMs int64 // Unix milliseconds, UTC
}

func (r *repo) PutHuman(ctx context.Context, h Human) error {
	return mapErr(r.writes.InsertHuman(ctx, sqlcgen.InsertHumanParams{
		ID: h.ID, Username: h.Username, CreatedAt: h.CreatedAtMs,
	}))
}

func (r *repo) GetHumanByUsername(ctx context.Context, username string) (Human, error) {
	row, err := r.reads.GetHumanByUsername(ctx, username)
	if err != nil {
		return Human{}, mapErr(err)
	}
	return Human{ID: row.ID, Username: row.Username, CreatedAtMs: row.CreatedAt}, nil
}

func (r *repo) ListHumans(ctx context.Context) ([]Human, error) {
	rows, err := r.reads.ListHumans(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Human, 0, len(rows))
	for _, row := range rows {
		out = append(out, Human{ID: row.ID, Username: row.Username, CreatedAtMs: row.CreatedAt})
	}
	return out, nil
}
