package main

import (
	"context"
	"fmt"
	"io"
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
