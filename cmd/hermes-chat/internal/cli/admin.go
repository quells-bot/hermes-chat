// Package cli implements the hermes-chat admin subcommands: migrate, user,
// and agent. It wires the bus against the database itself; the hub and the
// HTTP controller belong to serve.
package cli

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

// Migrate applies the database migrations and reports on stdout.
func Migrate(ctx context.Context, dbPath string, stdout io.Writer) error {
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "migrations applied")
	return nil
}

// openSvc opens dbPath, requires the schema to be current, and returns the
// service and a cleanup func. The caller must run the cleanup, on error too.
func openSvc(ctx context.Context, dbPath string) (bus.Service, func(), error) {
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open database: %w", err)
	}
	cleanup := func() { _ = db.Close() }
	if err := db.CheckSchema(ctx); err != nil {
		if errors.Is(err, storage.ErrSchemaBehind) {
			err = errors.New(`database schema is out of date; run "hermes-chat migrate"`)
		}
		return nil, cleanup, err
	}
	return bus.NewService(storage.NewRepo(db), time.Now), cleanup, nil
}

// report prints err and returns the process exit code.
func report(stderr io.Writer, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "hermes-chat: %v\n", err)
		return 1
	}
	return 0
}

const userUsage = `usage: hermes-chat user <subcommand>

subcommands:
  add <username>      create a user; reads the password from the terminal or stdin
  passwd <username>   set a user's password, ending their sessions
  remove <username>   delete a user
  list                list users
`

// User runs the user subcommands and returns the process exit code.
func User(ctx context.Context, dbPath string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 2 && args[0] == "add":
		return report(stderr, userAdd(ctx, dbPath, args[1], stdin, stdout, stderr))
	case len(args) == 2 && args[0] == "passwd":
		return report(stderr, userPasswd(ctx, dbPath, args[1], stdin, stdout, stderr))
	case len(args) == 2 && args[0] == "remove":
		return report(stderr, userRemove(ctx, dbPath, args[1], stdout))
	case len(args) == 1 && args[0] == "list":
		return report(stderr, userList(ctx, dbPath, stdout))
	}
	fmt.Fprint(stderr, userUsage)
	return 2
}

func userAdd(ctx context.Context, dbPath string, username string, stdin io.Reader, stdout, stderr io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath) // before the prompt, so an unmigrated database is reported first
	defer cleanup()
	if err != nil {
		return err
	}
	password, err := readPassword(stdin, stderr)
	if err != nil {
		return err
	}
	h, err := svc.CreateHuman(ctx, username, password)
	if errors.Is(err, bus.ErrConflict) {
		return fmt.Errorf("user %q already exists", strings.ToLower(strings.TrimSpace(username)))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created user %s (%s)\n", h.Username, h.ID)
	return nil
}

func userPasswd(ctx context.Context, dbPath string, username string, stdin io.Reader, stdout, stderr io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	password, err := readPassword(stdin, stderr)
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(username))
	err = svc.SetHumanPassword(ctx, username, password)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("user %q not found", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "password updated for %s\n", name)
	return nil
}

func userRemove(ctx context.Context, dbPath string, username string, stdout io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(username))
	err = svc.RemoveHuman(ctx, username)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("user %q not found", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed user %s\n", name)
	return nil
}

func userList(ctx context.Context, dbPath string, stdout io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	humans, err := svc.ListHumans(ctx)
	if err != nil {
		return err
	}
	for _, h := range humans {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", h.ID, h.Username, h.CreatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}