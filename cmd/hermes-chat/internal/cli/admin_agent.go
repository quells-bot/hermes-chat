package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

const agentUsage = `usage: hermes-chat agent <subcommand>

subcommands:
  add <handle> <display name>   create an agent and print its token once
  revoke <handle>               end an agent's token
  rotate <handle>               issue a new token, ending the old one
  list                          list agents
`

// Agent runs the agent subcommands and returns the process exit code.
func Agent(ctx context.Context, dbPath string, args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 3 && args[0] == "add":
		return report(stderr, agentAdd(ctx, dbPath, args[1], args[2], stdout))
	case len(args) == 2 && args[0] == "revoke":
		return report(stderr, agentRevoke(ctx, dbPath, args[1], stdout))
	case len(args) == 2 && args[0] == "rotate":
		return report(stderr, agentRotate(ctx, dbPath, args[1], stdout))
	case len(args) == 1 && args[0] == "list":
		return report(stderr, agentList(ctx, dbPath, stdout))
	}
	fmt.Fprint(stderr, agentUsage)
	return 2
}

// displayHandle is the handle as the bus will normalize it, for messages.
func displayHandle(handle string) string {
	return "@" + strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
}

func agentAdd(ctx context.Context, dbPath, handle, displayName string, stdout io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	a, token, err := svc.CreateAgent(ctx, handle, displayName)
	if errors.Is(err, bus.ErrConflict) {
		return fmt.Errorf("agent %q already exists", displayHandle(handle))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created agent @%s (%s)\ntoken: %s\n", a.Handle, a.ID, token)
	return nil
}

func agentRevoke(ctx context.Context, dbPath, handle string, stdout io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	err = svc.RevokeAgent(ctx, handle)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("agent %q not found", displayHandle(handle))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "revoked agent %s\n", displayHandle(handle))
	return nil
}

func agentRotate(ctx context.Context, dbPath, handle string, stdout io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	token, err := svc.RotateAgentToken(ctx, handle)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("agent %q not found", displayHandle(handle))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "rotated token for %s\ntoken: %s\n", displayHandle(handle), token)
	return nil
}

func agentList(ctx context.Context, dbPath string, stdout io.Writer) error {
	svc, cleanup, err := openSvc(ctx, dbPath)
	defer cleanup()
	if err != nil {
		return err
	}
	agents, err := svc.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, a := range agents {
		state := "active"
		if a.Revoked() {
			state = "revoked"
		}
		fmt.Fprintf(stdout, "%s\t@%s\t%s\t%s\t%s\n", a.ID, a.Handle, a.DisplayName, state, a.CreatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}