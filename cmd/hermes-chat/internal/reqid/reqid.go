// Package reqid carries a per-request ULID through contexts, gin, and slog.
package reqid

import (
	"context"
	"log/slog"

	"github.com/oklog/ulid/v2"
)

const (
	// GinKey is the key for gin's c.Set / c.Get.
	GinKey = "request_id"
	// LogKey is the slog attribute name.
	LogKey = "request_id"
)

type ctxKey struct{}

// NewContext returns a copy of ctx carrying id.
func NewContext(ctx context.Context, id ulid.ULID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request ID in ctx, if any.
func FromContext(ctx context.Context) (ulid.ULID, bool) {
	id, ok := ctx.Value(ctxKey{}).(ulid.ULID)
	return id, ok
}

// op is a deferred WithAttrs or WithGroup call.
type op struct {
	group string
	attrs []slog.Attr
}

type handler struct {
	base    slog.Handler // original, ungrouped inner handler
	ops     []op         // WithAttrs/WithGroup calls made so far
	derived slog.Handler // base with ops applied; used when there is no request ID
}

// NewHandler wraps inner so records logged with a context carrying a request
// ID get a top-level request_id attribute, even after WithGroup.
func NewHandler(inner slog.Handler) slog.Handler {
	return &handler{base: inner, derived: inner}
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.derived.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	id, ok := FromContext(ctx)
	if !ok {
		return h.derived.Handle(ctx, r)
	}
	inner := h.base.WithAttrs([]slog.Attr{slog.String(LogKey, id.String())})
	for _, o := range h.ops {
		if o.group != "" {
			inner = inner.WithGroup(o.group)
		} else {
			inner = inner.WithAttrs(o.attrs)
		}
	}
	return inner.Handle(ctx, r)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{
		base:    h.base,
		ops:     appendOp(h.ops, op{attrs: attrs}),
		derived: h.derived.WithAttrs(attrs),
	}
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &handler{
		base:    h.base,
		ops:     appendOp(h.ops, op{group: name}),
		derived: h.derived.WithGroup(name),
	}
}

// appendOp copies so sibling handlers never share a backing array.
func appendOp(ops []op, o op) []op {
	out := make([]op, len(ops), len(ops)+1)
	copy(out, ops)
	return append(out, o)
}
