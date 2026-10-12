// Package sse reads and writes Server-Sent Events frames. It imports no layer.
package sse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Frame is one event, or one comment when only Comment is set.
type Frame struct {
	ID      string // "" writes no id line
	Event   string // "" writes no event line
	Data    string // may hold newlines
	Comment string
}

// commentOnly reports whether f is a comment and nothing else.
func (f Frame) commentOnly() bool {
	return f.Comment != "" && f.ID == "" && f.Event == "" && f.Data == ""
}

// Write writes f to w in a single call: the comment, then the id, the event,
// and one data line for each line of Data, then a blank line. A comment alone
// writes no data line; any other frame writes at least one. An ID, Event, or
// Comment holding a line break is an error, and nothing is written.
func Write(w io.Writer, f Frame) error {
	for _, field := range [][2]string{{"id", f.ID}, {"event", f.Event}, {"comment", f.Comment}} {
		if strings.ContainsAny(field[1], "\r\n") {
			return fmt.Errorf("sse: %s must not contain a line break", field[0])
		}
	}
	var b bytes.Buffer
	if f.Comment != "" {
		b.WriteString(": " + f.Comment + "\n")
	}
	if !f.commentOnly() {
		if f.ID != "" {
			b.WriteString("id: " + f.ID + "\n")
		}
		if f.Event != "" {
			b.WriteString("event: " + f.Event + "\n")
		}
		for line := range strings.SplitSeq(f.Data, "\n") {
			b.WriteString("data: " + line + "\n")
		}
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}

// Decoder reads frames from a stream.
type Decoder struct {
	r *bufio.Reader
}

// NewDecoder returns a Decoder reading from r. Lines may be of any length.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// Next returns the next frame, comment-only frames included. It returns io.EOF
// at the end of the stream; a frame the stream ends in the middle of is dropped.
// Lines may end in "\n" or "\r\n". Fields other than id, event, and data are
// ignored.
func (d *Decoder) Next() (Frame, error) {
	var (
		f        Frame
		data     []string
		comments []string
		started  bool // a line of this frame has been read
	)
	for {
		line, err := d.r.ReadString('\n')
		if err != nil {
			// Whatever was read is an unfinished line of an unfinished frame.
			return Frame{}, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if !started {
				continue
			}
			f.Data = strings.Join(data, "\n")
			f.Comment = strings.Join(comments, "\n")
			return f, nil
		}
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "":
			comments = append(comments, value)
		case "id":
			f.ID = value
		case "event":
			f.Event = value
		case "data":
			data = append(data, value)
		default:
			continue
		}
		started = true
	}
}
