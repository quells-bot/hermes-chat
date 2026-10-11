package storage

import (
	"context"
	"database/sql"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Agent is a non-human participant that authenticates with a bearer token.
type Agent struct {
	ID          string // ULID text
	Handle      string
	DisplayName string
	TokenHash   string
	RevokedAtMs *int64 // nil while live
	CreatedAtMs int64  // Unix milliseconds, UTC
}

func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func agentFromRow(row sqlcgen.Agent) Agent {
	a := Agent{
		ID: row.ID, Handle: row.Handle, DisplayName: row.DisplayName,
		TokenHash: row.TokenHash, CreatedAtMs: row.CreatedAt,
	}
	if row.RevokedAt.Valid {
		v := row.RevokedAt.Int64
		a.RevokedAtMs = &v
	}
	return a
}

func (r *repo) PutAgent(ctx context.Context, a Agent) error {
	return mapErr(r.writes.InsertAgent(ctx, sqlcgen.InsertAgentParams{
		ID: a.ID, Handle: a.Handle, DisplayName: a.DisplayName, TokenHash: a.TokenHash,
		RevokedAt: nullInt64(a.RevokedAtMs), CreatedAt: a.CreatedAtMs,
	}))
}

func (r *repo) GetAgentByID(ctx context.Context, id string) (Agent, error) {
	row, err := r.reads.GetAgentByID(ctx, id)
	if err != nil {
		return Agent{}, mapErr(err)
	}
	return agentFromRow(row), nil
}

func (r *repo) GetAgentByHandle(ctx context.Context, handle string) (Agent, error) {
	row, err := r.reads.GetAgentByHandle(ctx, handle)
	if err != nil {
		return Agent{}, mapErr(err)
	}
	return agentFromRow(row), nil
}

func (r *repo) GetAgentByTokenHash(ctx context.Context, hash string) (Agent, error) {
	row, err := r.reads.GetAgentByTokenHash(ctx, hash)
	if err != nil {
		return Agent{}, mapErr(err)
	}
	return agentFromRow(row), nil
}

func (r *repo) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := r.reads.ListAgents(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentFromRow(row))
	}
	return out, nil
}

func (r *repo) SetAgentTokenHash(ctx context.Context, id, hash string) error {
	return requireRow(r.writes.SetAgentTokenHash(ctx, sqlcgen.SetAgentTokenHashParams{TokenHash: hash, ID: id}))
}

func (r *repo) SetAgentRevokedAt(ctx context.Context, id string, revokedAtMs *int64) error {
	return requireRow(r.writes.SetAgentRevokedAt(ctx, sqlcgen.SetAgentRevokedAtParams{RevokedAt: nullInt64(revokedAtMs), ID: id}))
}
