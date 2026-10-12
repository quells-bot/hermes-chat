// Command hermes-chat is the Hermes Chat server and its admin tooling.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/cli"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// command runs with a loaded config and returns the process exit code. A
// command reports its own usage errors by returning 2.
type command func(ctx context.Context, cfg Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int

// subcommand is one entry of the command list that drives both dispatch and usage.
type subcommand struct {
	name, summary string
	run           command
}

// commandList is the single ordered list of subcommands. It is a function, not
// a variable, because the commands print usage, which reads the list.
func commandList() []subcommand {
	return []subcommand{
		{"serve", "run the HTTP server", func(ctx context.Context, cfg Config, args []string, _ io.Reader, _, stderr io.Writer) int {
			if len(args) > 0 {
				fmt.Fprint(stderr, usage())
				return 2
			}
			if err := cfg.validateForServe(); err != nil {
				return report(stderr, err)
			}
			return report(stderr, serve(ctx, cfg, newLogger(stderr, cfg.slogLevel()), listeners{}))
		}},
		{"migrate", "apply database migrations", func(ctx context.Context, cfg Config, args []string, _ io.Reader, stdout, stderr io.Writer) int {
			if len(args) > 0 {
				fmt.Fprint(stderr, usage())
				return 2
			}
			return report(stderr, cli.Migrate(ctx, cfg.DBPath, stdout))
		}},
		{"user", "manage users (add, passwd, remove, list)", func(ctx context.Context, cfg Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
			return cli.User(ctx, cfg.DBPath, args, stdin, stdout, stderr)
		}},
		{"agent", "manage agents (add, revoke, rotate, list)", func(ctx context.Context, cfg Config, args []string, _ io.Reader, stdout, stderr io.Writer) int {
			return cli.Agent(ctx, cfg.DBPath, args, stdout, stderr)
		}},
	}
}

func usage() string {
	var b strings.Builder
	b.WriteString("usage: hermes-chat <command>\n\ncommands:\n")
	for _, c := range commandList() {
		fmt.Fprintf(&b, "  %-9s %s\n", c.name, c.summary)
	}
	return b.String()
}

func report(stderr io.Writer, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "hermes-chat: %v\n", err)
		return 1
	}
	return 0
}

// run dispatches a subcommand and returns the process exit code. Usage errors
// (exit 2) are reported before configuration is loaded.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())
		return 2
	}
	var cmd command
	for _, c := range commandList() {
		if c.name == args[0] {
			cmd = c.run
		}
	}
	if cmd == nil {
		fmt.Fprintf(stderr, "hermes-chat: unknown command %q\n\n%s", args[0], usage())
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		return report(stderr, err)
	}
	return cmd(ctx, cfg, args[1:], stdin, stdout, stderr)
}
