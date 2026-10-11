package storage

import (
	"context"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Session is a logged-in human's browser session, identified by a token hash.
type Session struct {
	ID          string // ULID text
	TokenHash   string
	HumanID     string
	CreatedAtMs int64 // Unix milliseconds, UTC
	ExpiresAtMs int64 // Unix milliseconds, UTC
}

func (r *repo) PutSession(ctx context.Context, s Session) error {
	return mapErr(r.writes.InsertSession(ctx, sqlcgen.InsertSessionParams{
		ID: s.ID, TokenHash: s.TokenHash, HumanID: s.HumanID,
		CreatedAt: s.CreatedAtMs, ExpiresAt: s.ExpiresAtMs,
	}))
}

func (r *repo) GetSessionByTokenHash(ctx context.Context, hash string) (Session, error) {
	row, err := r.reads.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return Session{}, mapErr(err)
	}
	return Session{
		ID: row.ID, TokenHash: row.TokenHash, HumanID: row.HumanID,
		CreatedAtMs: row.CreatedAt, ExpiresAtMs: row.ExpiresAt,
	}, nil
}

func (r *repo) DeleteSessionByTokenHash(ctx context.Context, hash string) error {
	return mapErr(r.writes.DeleteSessionByTokenHash(ctx, hash))
}

func (r *repo) DeleteSessionsByHuman(ctx context.Context, humanID string) error {
	return mapErr(r.writes.DeleteSessionsByHuman(ctx, humanID))
}

func (r *repo) DeleteExpiredSessions(ctx context.Context, nowMs int64) error {
	return mapErr(r.writes.DeleteExpiredSessions(ctx, nowMs))
}
