// Package web holds the built web UI. It is a leaf: it imports none of the
// server's layers, and only cmd/hermes-chat imports it.
package web

import (
	"embed"
	"io/fs"
)

// all: because a fresh clone's dist holds only .gitkeep, and Vite may emit
// chunks whose names start with an underscore.
//
//go:embed all:dist
var dist embed.FS

// Dist returns the built files: index.html and assets/. Before the UI has
// been built it holds only a placeholder.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // a fixed, valid path
	}
	return sub
}
