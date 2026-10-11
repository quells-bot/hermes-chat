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

// run dispatches a subcommand and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve", "migrate":
	default:
		fmt.Fprintf(stderr, "hermes-chat: unknown command %q\n\n%s", args[0], usage)
		return 2
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "hermes-chat: %v\n", err)
		return 1
	}
	log := newLogger(stderr, cfg.slogLevel())

	switch args[0] {
	case "serve":
		err = serve(ctx, cfg, log, listeners{})
	case "migrate":
		err = migrate(ctx, cfg, stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "hermes-chat: %v\n", err)
		return 1
	}
	return 0
}
