package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jsonLines decodes one JSON object per log line.
func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, s := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if s == "" {
			continue
		}
		m := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(s), &m))
		out = append(out, m)
	}
	return out
}
