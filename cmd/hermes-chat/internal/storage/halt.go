package storage

import (
	"context"
)

func (r *repo) GetHaltedAtMs(ctx context.Context) (*int64, error) {
	v, err := r.reads.GetHaltedAt(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	if !v.Valid {
		return nil, nil
	}
	return &v.Int64, nil
}

func (r *repo) SetHaltedAtMs(ctx context.Context, haltedAtMs *int64) error {
	return requireRow(r.writes.SetHaltedAt(ctx, nullInt64(haltedAtMs)))
}
