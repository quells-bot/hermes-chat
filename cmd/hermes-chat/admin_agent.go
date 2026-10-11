package main

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

// agent dispatches the agent subcommands.
func agent(ctx context.Context, cfg Config, args []string, _ io.Reader, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 3 && args[0] == "add":
		return report(stderr, agentAdd(ctx, cfg, args[1], args[2], stdout))
	case len(args) == 2 && args[0] == "revoke":
		return report(stderr, agentRevoke(ctx, cfg, args[1], stdout))
	case len(args) == 2 && args[0] == "rotate":
		return report(stderr, agentRotate(ctx, cfg, args[1], stdout))
	case len(args) == 1 && args[0] == "list":
		return report(stderr, agentList(ctx, cfg, stdout))
	}
	fmt.Fprint(stderr, agentUsage)
	return 2
}

// displayHandle is the handle as the bus will normalize it, for messages.
func displayHandle(handle string) string {
	return "@" + strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
}

func agentAdd(ctx context.Context, cfg Config, handle, displayName string, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	a, token, err := w.Svc.CreateAgent(ctx, handle, displayName)
	if errors.Is(err, bus.ErrConflict) {
		return fmt.Errorf("agent %q already exists", displayHandle(handle))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created agent @%s (%s)\ntoken: %s\n", a.Handle, a.ID, token)
	return nil
}

func agentRevoke(ctx context.Context, cfg Config, handle string, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	err = w.Svc.RevokeAgent(ctx, handle)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("agent %q not found", displayHandle(handle))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "revoked agent %s\n", displayHandle(handle))
	return nil
}

func agentRotate(ctx context.Context, cfg Config, handle string, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	token, err := w.Svc.RotateAgentToken(ctx, handle)
	if errors.Is(err, bus.ErrNotFound) {
		return fmt.Errorf("agent %q not found", displayHandle(handle))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "rotated token for %s\ntoken: %s\n", displayHandle(handle), token)
	return nil
}

func agentList(ctx context.Context, cfg Config, stdout io.Writer) error {
	w, err := checkedWires(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}
	agents, err := w.Svc.ListAgents(ctx)
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
