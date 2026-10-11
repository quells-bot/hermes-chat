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
  add <username>      create a user; reads the password from the terminal or stdin
  passwd <username>   set a user's password, ending their sessions
  remove <username>   delete a user
  list                list users
`

// user dispatches the user subcommands.
func user(ctx context.Context, cfg Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 2 && args[0] == "add":
		return report(stderr, userAdd(ctx, cfg, args[1], stdin, stdout, stderr))
	case len(args) == 2 && args[0] == "passwd":
		return report(stderr, userPasswd(ctx, cfg, args[1], stdin, stdout, stderr))
	case len(args) == 2 && args[0] == "remove":
		return report(stderr, userRemove(ctx, cfg, args[1], stdout))
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

func userAdd(ctx context.Context, cfg Config, username string, stdin io.Reader, stdout, stderr io.Writer) error {
	w, err := checkedWires(ctx, cfg) // before the prompt, so an unmigrated database is reported first
	defer w.cleanUp()
	if err != nil {
		return err
	}
	password, err := readPassword(stdin, stderr)
	if err != nil {
		return err
	}
	h, err := w.Svc.CreateHuman(ctx, username, password)
	if errors.Is(err, bus.ErrConflict) {
		return fmt.Errorf("user %q already exists", strings.ToLower(strings.TrimSpace(username)))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created user %s (%s)\n", h.Username, h.ID)
	return nil
}

func userPasswd(ctx context.Context, cfg Config, username string, stdin io.Reader, stdout, stderr io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	password, err := readPassword(stdin, stderr)
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(username))
	err = w.Svc.SetHumanPassword(ctx, username, password)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("user %q not found", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "password updated for %s\n", name)
	return nil
}

func userRemove(ctx context.Context, cfg Config, username string, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(username))
	err = w.Svc.RemoveHuman(ctx, username)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("user %q not found", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed user %s\n", name)
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
