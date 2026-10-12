package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Files stores attachment bodies as files in one directory, each named by its
// attachment id.
type Files struct {
	dir string
}

// OpenFiles returns a store on dir, creating it (0700) if it is missing.
func OpenFiles(dir string) (*Files, error) {
	if err := os.MkdirAll(dir, os.FileMode(0o700)); err != nil {
		return nil, fmt.Errorf("make attachments dir: %w", err)
	}
	return &Files{dir: dir}, nil
}

// validAttachmentID accepts exactly what the store can name a file with: not
// empty, not "." or "..", and no path element separators. An id is a name,
// never a path.
func validAttachmentID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// Create writes all of r to the attachment's file, returning its size. It
// writes to a temporary file first, so a failed or cancelled write leaves
// nothing behind.
func (f *Files) Create(ctx context.Context, id string, r io.Reader) (int64, error) {
	if !validAttachmentID(id) {
		return 0, fmt.Errorf("attachment id %q is not a plain name", id)
	}
	tmp := filepath.Join(f.dir, id+".tmp")
	final := filepath.Join(f.dir, id)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(0o600))
	if err != nil {
		return 0, fmt.Errorf("open attachment temp: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = file.Close()
			_ = os.Remove(tmp)
		}
	}()
	var total int64 = 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		buf := make([]byte, 1 << 16)
		n, err := r.Read(buf)
		if errors.Is(err, io.EOF) {
			break // end of input, as the io contract allows
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			break
		}
		if _, err := file.Write(buf[:n]); err != nil {
			return 0, err
		}
		total += int64(n)
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	done = true
	return total, nil
}

// Open returns the attachment's file, or an error wrapping fs.ErrNotExist when
// there is none.
func (f *Files) Open(id string) (io.ReadSeekCloser, error) {
	if !validAttachmentID(id) {
		return nil, fmt.Errorf("attachment id %q is not a plain name", id)
	}
	file, err := os.Open(filepath.Join(f.dir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("attachment %s: %w", id, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Remove deletes the attachment's file; removing one that is not there is not
// an error.
func (f *Files) Remove(id string) error {
	if !validAttachmentID(id) {
		return fmt.Errorf("attachment id %q is not a plain name", id)
	}
	err := os.Remove(filepath.Join(f.dir, id))
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}