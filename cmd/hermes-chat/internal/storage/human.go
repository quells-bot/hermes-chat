package storage

import (
	"context"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Human is a registered user.
type Human struct {
	ID           string // ULID text
	Username     string
	PasswordHash string // "" means no password: this human cannot log in
	CreatedAtMs  int64  // Unix milliseconds, UTC
}

func (r *repo) PutHuman(ctx context.Context, h Human) error {
	return mapErr(r.writes.InsertHuman(ctx, sqlcgen.InsertHumanParams{
		ID: h.ID, Username: h.Username, PasswordHash: h.PasswordHash, CreatedAt: h.CreatedAtMs,
	}))
}

func (r *repo) GetHumanByUsername(ctx context.Context, username string) (Human, error) {
	row, err := r.reads.GetHumanByUsername(ctx, username)
	if err != nil {
		return Human{}, mapErr(err)
	}
	return humanFromRow(row), nil
}

func (r *repo) GetHumanByID(ctx context.Context, id string) (Human, error) {
	row, err := r.reads.GetHumanByID(ctx, id)
	if err != nil {
		return Human{}, mapErr(err)
	}
	return humanFromRow(row), nil
}

func (r *repo) SetHumanPasswordHash(ctx context.Context, id, hash string) error {
	return requireRow(r.writes.SetHumanPasswordHash(ctx, sqlcgen.SetHumanPasswordHashParams{PasswordHash: hash, ID: id}))
}

func (r *repo) DeleteHuman(ctx context.Context, id string) error {
	return requireRow(r.writes.DeleteHuman(ctx, id))
}

func humanFromRow(row sqlcgen.Human) Human {
	return Human{ID: row.ID, Username: row.Username, PasswordHash: row.PasswordHash, CreatedAtMs: row.CreatedAt}
}

func (r *repo) ListHumans(ctx context.Context) ([]Human, error) {
	rows, err := r.reads.ListHumans(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Human, 0, len(rows))
	for _, row := range rows {
		out = append(out, humanFromRow(row))
	}
	return out, nil
}
