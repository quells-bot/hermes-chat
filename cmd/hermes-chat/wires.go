package main

import (
	"context"
	"fmt"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)


type wires struct {
	Hub     *bus.Hub
	App     app.Controller
	CleanUp []func() error
}

// wireUp opens the database, applies the migrations, and constructs the layers
// serve needs. On failure it still returns the clean-up functions collected so far.
func wireUp(ctx context.Context, cfg Config) (wires, error) {
	var w wires
	db, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return w, fmt.Errorf("open database: %w", err)
	}
	w.CleanUp = append(w.CleanUp, db.Close)

	if err := db.Migrate(ctx); err != nil {
		return w, err
	}

	svc := bus.NewService(storage.NewRepo(db), time.Now)
	w.Hub = svc.Hub()
	w.App = app.NewController(svc, app.Options{CookieSecure: cfg.CookieSecure})
	return w, nil
}

// cleanUp runs the clean-up functions in reverse order, ignoring errors.
func (w wires) cleanUp() {
	for i := len(w.CleanUp) - 1; i >= 0; i-- {
		_ = w.CleanUp[i]()
	}
}
