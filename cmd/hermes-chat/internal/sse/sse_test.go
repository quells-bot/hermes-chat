package sse_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/sse"
)

func TestWrite(t *testing.T) {
	cases := []struct {
		f    sse.Frame
		want string
	}{
		{sse.Frame{ID: "7", Event: "message.posted", Data: `{"a":1}`}, "id: 7\nevent: message.posted\ndata: {\"a\":1}\n\n"},
		{sse.Frame{Data: "one\ntwo"}, "data: one\ndata: two\n\n"},
		{sse.Frame{Event: "ready"}, "event: ready\ndata: \n\n"},
		{sse.Frame{Comment: "hb"}, ": hb\n\n"},
		{sse.Frame{Comment: "note", Data: "x"}, ": note\ndata: x\n\n"},
	}
	for _, tc := range cases {
		var b strings.Builder
		require.NoError(t, sse.Write(&b, tc.f))
		assert.Equal(t, tc.want, b.String())
	}
	for _, bad := range []sse.Frame{{ID: "1\n2", Data: "x"}, {Event: "a\rb", Data: "x"}} {
		var b strings.Builder
		assert.Error(t, sse.Write(&b, bad))
		assert.Empty(t, b.String())
	}
}

func TestDecoder(t *testing.T) {
	in := ": hb\n\nid: 7\nevent: message.posted\ndata: one\ndata:two\n\r\ndata: crlf\r\n\r\nid: 9\ndata: unfinished\n"
	d := sse.NewDecoder(strings.NewReader(in))
	for _, want := range []sse.Frame{{Comment: "hb"}, {ID: "7", Event: "message.posted", Data: "one\ntwo"}, {Data: "crlf"}} {
		got, err := d.Next()
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := d.Next()
	assert.ErrorIs(t, err, io.EOF) // the unfinished frame is dropped
}

func TestRoundTrip_OneLineOfAMebibyte(t *testing.T) {
	big := sse.Frame{ID: "1", Event: "message.posted", Data: strings.Repeat("a", 1<<20)}
	var b bytes.Buffer
	require.NoError(t, sse.Write(&b, big))
	require.NoError(t, sse.Write(&b, sse.Frame{Data: "next"}))
	d := sse.NewDecoder(&b)
	got, err := d.Next()
	require.NoError(t, err)
	assert.Equal(t, big, got)
	got, err = d.Next()
	require.NoError(t, err)
	assert.Equal(t, "next", got.Data)
}
