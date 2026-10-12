package main

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quells/grace"
	"github.com/quells/grace/pkg/httpserver"
)

// listeners optionally supplies pre-bound listeners; nil listens on the configured address.
type listeners struct{ api, debug net.Listener }

func serve(ctx context.Context, cfg Config, log *slog.Logger, ln listeners) error {
	slog.SetDefault(log)

	w, err := wireUp(ctx, cfg)
	defer w.cleanUp()
	if err != nil {
		return err
	}

	gin.SetMode(cfg.GinMode)
	e := gin.New()
	if err := e.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return err
	}
	routes(e, w.App, log)

	apiLn, err := apiListener(cfg, ln.api)
	if err != nil {
		return err
	}
	apiOpts := []httpserver.Option{httpserver.WithLogger(log), httpserver.WithLoggerLabel("api")}
	if apiLn != nil {
		apiOpts = append(apiOpts, httpserver.WithListener(apiLn))
	}
	debugOpts := []httpserver.Option{
		httpserver.WithLogger(log), httpserver.WithLoggerLabel("debug"),
		httpserver.WithWriteTimeout(5 * time.Minute),
	}
	if ln.debug != nil {
		debugOpts = append(debugOpts, httpserver.WithListener(ln.debug))
	}

	ctx, stop := grace.StopOnInterrupt(ctx)
	defer stop()
	return grace.Run(ctx,
		httpserver.New(cfg.HTTPAddr, e, apiOpts...),
		httpserver.New(cfg.DebugAddr, httpserver.DebugHandler(), debugOpts...),
		w.Hub,
	)
}
