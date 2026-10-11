// Package storage owns the SQLite database: connection pools, pragmas, and migrations.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// pragmas are applied to every connection through the connection string.
var pragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"synchronous(NORMAL)",
	"cache_size(-64000)",
	"foreign_keys(true)",
	"temp_store(memory)",
}

// DB holds a single-connection write pool and a multi-connection read-only pool.
type DB struct {
	read, write *sql.DB
	path        string
}

func dsn(path string, extra ...string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	for _, e := range extra {
		k, v, _ := splitParam(e)
		q.Add(k, v)
	}
	return u.String() + "?" + q.Encode()
}

func splitParam(s string) (k, v string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func writeDSN(path string) string { return dsn(path, "_txlock=immediate") }

func readDSN(path string) string { return dsn(path, "_pragma=query_only(true)") }

// Open opens the write pool first (creating the file and switching it to WAL),
// then the read pool.
func Open(ctx context.Context, path string) (*DB, error) {
	write, err := sql.Open("sqlite", writeDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	if err := write.PingContext(ctx); err != nil {
		write.Close()
		return nil, fmt.Errorf("ping write pool: %w", err)
	}

	read, err := sql.Open("sqlite", readDSN(path))
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	read.SetMaxOpenConns(max(4, runtime.NumCPU()))
	if err := read.PingContext(ctx); err != nil {
		read.Close()
		write.Close()
		return nil, fmt.Errorf("ping read pool: %w", err)
	}
	return &DB{read: read, write: write, path: path}, nil
}

// Close closes both pools.
func (d *DB) Close() error {
	return errors.Join(d.read.Close(), d.write.Close())
}
