package main

import (
	"context"
	"fmt"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

type wireMode int

const (
	// wireMigrate brings the schema up to date.
	wireMigrate wireMode = iota
	// wireCheck fails with storage.ErrSchemaBehind if the schema is behind.
	wireCheck
)

type wires struct {
	Svc     bus.Service
	Hub     *bus.Hub
	App     app.Controller
	CleanUp []func() error
}

// wireUp opens the database and constructs the layers. On failure it still
// returns the clean-up functions collected so far.
func wireUp(ctx context.Context, cfg Config, mode wireMode) (wires, error) {
	var w wires
	db, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return w, fmt.Errorf("open database: %w", err)
	}
	w.CleanUp = append(w.CleanUp, db.Close)

	switch mode {
	case wireMigrate:
		err = db.Migrate(ctx)
	case wireCheck:
		err = db.CheckSchema(ctx)
	default:
		err = fmt.Errorf("unknown wire mode %d", mode)
	}
	if err != nil {
		return w, err
	}

	svc := bus.NewService(storage.NewRepo(db), time.Now)
	w.Svc = svc
	w.Hub = svc.Hub() // built in every mode; only serve runs it
	w.App = app.NewController(w.Svc, app.Options{CookieSecure: cfg.CookieSecure})
	return w, nil
}

// cleanUp runs the clean-up functions in reverse order, ignoring errors.
func (w wires) cleanUp() {
	for i := len(w.CleanUp) - 1; i >= 0; i-- {
		_ = w.CleanUp[i]()
	}
}
