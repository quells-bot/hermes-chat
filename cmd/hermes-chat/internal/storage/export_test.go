package storage

import "database/sql"

// Pools exposes the connection pools to black-box tests.
func (d *DB) Pools() (read, write *sql.DB) { return d.read, d.write }

// ReadDSN exposes the read pool connection string to black-box tests.
func ReadDSN(path string) string { return readDSN(path) }

// WriteDSN exposes the write pool connection string to black-box tests.
func WriteDSN(path string) string { return writeDSN(path) }

// Path exposes the path given to Open to black-box tests.
func (d *DB) Path() string { return d.path }
