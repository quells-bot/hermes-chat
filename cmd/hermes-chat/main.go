// Command hermes-chat is the Hermes Chat server and its admin tooling.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: hermes-chat <command>

commands:
  serve     run the HTTP server
  migrate   apply database migrations
`

// command runs with a loaded config and returns the process exit code. A
// command reports its own usage errors by returning 2.
type command func(ctx context.Context, cfg Config, args []string, stdout, stderr io.Writer) int

// commands is the single list of subcommands.
var commands = map[string]command{
	"serve": func(ctx context.Context, cfg Config, _ []string, _, stderr io.Writer) int {
		return report(stderr, serve(ctx, cfg, newLogger(stderr, cfg.slogLevel()), listeners{}))
	},
	"migrate": func(ctx context.Context, cfg Config, _ []string, stdout, stderr io.Writer) int {
		return report(stderr, migrate(ctx, cfg, stdout))
	},
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
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "hermes-chat: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		return report(stderr, err)
	}
	return cmd(ctx, cfg, args[1:], stdout, stderr)
}
