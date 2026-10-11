package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const agentTokenPrefix = "hca_"

// Agent is a non-human participant that authenticates with a bearer token.
type Agent struct {
	ID          ulid.ULID
	Handle      string // without the @
	DisplayName string
	CreatedAt   time.Time // UTC, millisecond precision
	RevokedAt   time.Time // zero while live
}

// Revoked reports whether the agent's token has been revoked.
func (a Agent) Revoked() bool { return !a.RevokedAt.IsZero() }

// normalizeHandle trims, strips one leading @, lowercases, and reports whether
// the result is a valid handle.
func normalizeHandle(handle string) (string, bool) {
	handle = strings.TrimSpace(handle)
	handle = strings.ToLower(strings.TrimPrefix(handle, "@"))
	return handle, usernamePattern.MatchString(handle)
}

func validateDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 64 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", ValidationError{Message: "display name must be 1-64 characters with no control characters"}
	}
	return name, nil
}

func agentFromRow(row storage.Agent) (Agent, error) {
	id, err := ulid.ParseStrict(row.ID)
	if err != nil {
		return Agent{}, fmt.Errorf("stored agent %q has an invalid id: %w", row.Handle, err)
	}
	a := Agent{ID: id, Handle: row.Handle, DisplayName: row.DisplayName, CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC()}
	if row.RevokedAtMs != nil {
		a.RevokedAt = time.UnixMilli(*row.RevokedAtMs).UTC()
	}
	return a, nil
}

// CreateAgent registers an agent and returns its token, which is not stored
// and cannot be recovered later.
func (s *service) CreateAgent(ctx context.Context, handle, displayName string) (Agent, string, error) {
	handle, ok := normalizeHandle(handle)
	if !ok {
		return Agent{}, "", ValidationError{Message: "handle must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit"}
	}
	displayName, err := validateDisplayName(displayName)
	if err != nil {
		return Agent{}, "", err
	}
	token, hash, err := newToken(agentTokenPrefix)
	if err != nil {
		return Agent{}, "", err
	}
	at := s.now()
	id, err := newID(at)
	if err != nil {
		return Agent{}, "", fmt.Errorf("generate id: %w", err)
	}
	err = s.repo.PutAgent(ctx, storage.Agent{ID: id.String(), Handle: handle, DisplayName: displayName, TokenHash: hash, CreatedAtMs: at.UnixMilli()})
	if errors.Is(err, storage.ErrConflict) {
		return Agent{}, "", fmt.Errorf("agent %q already exists: %w", handle, ErrConflict)
	}
	if err != nil {
		return Agent{}, "", fmt.Errorf("create agent: %w", err)
	}
	return Agent{ID: id, Handle: handle, DisplayName: displayName, CreatedAt: at}, token, nil
}

// RotateAgentToken issues a new token, which ends the old one, and makes a
// revoked agent live again.
func (s *service) RotateAgentToken(ctx context.Context, handle string) (string, error) {
	handle, ok := normalizeHandle(handle)
	if !ok {
		return "", fmt.Errorf("agent %q: %w", handle, ErrNotFound)
	}
	token, hash, err := newToken(agentTokenPrefix)
	if err != nil {
		return "", err
	}
	err = s.repo.InTx(ctx, func(r storage.Repository) error {
		row, err := r.GetAgentByHandle(ctx, handle)
		if err != nil {
			return err
		}
		if err := r.SetAgentTokenHash(ctx, row.ID, hash); err != nil {
			return err
		}
		return r.SetAgentRevokedAt(ctx, row.ID, nil)
	})
	if errors.Is(err, storage.ErrNotFound) {
		return "", fmt.Errorf("agent %q: %w", handle, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("rotate agent token: %w", err)
	}
	return token, nil
}

// RevokeAgent ends an agent's token. Revoking a revoked agent keeps the first
// revocation time.
func (s *service) RevokeAgent(ctx context.Context, handle string) error {
	handle, ok := normalizeHandle(handle)
	if !ok {
		return fmt.Errorf("agent %q: %w", handle, ErrNotFound)
	}
	at := s.now().UnixMilli()
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		row, err := r.GetAgentByHandle(ctx, handle)
		if err != nil {
			return err
		}
		if row.RevokedAtMs != nil {
			return nil
		}
		return r.SetAgentRevokedAt(ctx, row.ID, &at)
	})
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("agent %q: %w", handle, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("revoke agent: %w", err)
	}
	return nil
}

// ListAgents returns all agents ordered by handle.
func (s *service) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.repo.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		a, err := agentFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// GetAgent returns the agent with the given id, or ErrNotFound.
func (s *service) GetAgent(ctx context.Context, id ulid.ULID) (Agent, error) {
	row, err := s.repo.GetAgentByID(ctx, id.String())
	if errors.Is(err, storage.ErrNotFound) {
		return Agent{}, fmt.Errorf("agent %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Agent{}, fmt.Errorf("get agent: %w", err)
	}
	return agentFromRow(row)
}

// ResolveAgentToken returns the agent actor a token belongs to. An empty,
// unknown, or revoked token is ErrUnauthenticated.
func (s *service) ResolveAgentToken(ctx context.Context, token string) (Actor, error) {
	if token == "" {
		return Actor{}, ErrUnauthenticated
	}
	row, err := s.repo.GetAgentByTokenHash(ctx, hashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return Actor{}, ErrUnauthenticated
	}
	if err != nil {
		return Actor{}, fmt.Errorf("resolve agent token: %w", err)
	}
	if row.RevokedAtMs != nil {
		return Actor{}, ErrUnauthenticated
	}
	a, err := agentFromRow(row)
	if err != nil {
		return Actor{}, err
	}
	return Actor{Kind: ActorAgent, ID: a.ID}, nil
}
