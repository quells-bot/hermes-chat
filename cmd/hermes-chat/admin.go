package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func migrate(ctx context.Context, cfg Config, stdout io.Writer) error {
	w, err := wireUp(ctx, cfg, wireMigrate)
	w.cleanUp()
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "migrations applied")
	return nil
}

const userUsage = `usage: hermes-chat user <subcommand>

subcommands:
  add <username>   create a user
  list             list users
`

// user dispatches the user subcommands.
func user(ctx context.Context, cfg Config, args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 2 && args[0] == "add":
		return report(stderr, userAdd(ctx, cfg, args[1], stdout))
	case len(args) == 1 && args[0] == "list":
		return report(stderr, userList(ctx, cfg, stdout))
	}
	fmt.Fprint(stderr, userUsage)
	return 2
}

// checkedWires wires the layers against an already-migrated database. The
// caller must call cleanUp on the result, even on error.
func checkedWires(ctx context.Context, cfg Config) (wires, error) {
	w, err := wireUp(ctx, cfg, wireCheck)
	if errors.Is(err, storage.ErrSchemaBehind) {
		err = errors.New(`database schema is out of date; run "hermes-chat migrate"`)
	}
	return w, err
}

func userAdd(ctx context.Context, cfg Config, username string, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	h, err := w.Svc.CreateHuman(ctx, username)
	if errors.Is(err, bus.ErrConflict) {
		return fmt.Errorf("user %q already exists", strings.ToLower(strings.TrimSpace(username)))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created user %s (%s)\n", h.Username, h.ID)
	return nil
}

func userList(ctx context.Context, cfg Config, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	humans, err := w.Svc.ListHumans(ctx)
	if err != nil {
		return err
	}
	for _, h := range humans {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", h.ID, h.Username, h.CreatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}
