package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	I = "github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal"
	S = "github.com/quells-bot/hermes-chat/internal" // shared leaves; no rules apply to them
)

func TestCheckLayering_Violations(t *testing.T) {
	cases := []pkgImports{
		{I + "/app", []string{I + "/storage"}},
		{I + "/app", []string{I + "/storage/sqlcgen"}},
		{I + "/bus", []string{I + "/app"}},
		{I + "/bus", []string{"github.com/gin-gonic/gin"}},
		{I + "/bus", []string{"net/http"}},
		{I + "/bus", []string{I + "/storage/sqlcgen"}},
		{I + "/storage", []string{I + "/bus"}},
		{I + "/storage/storagetest", []string{I + "/app"}},
		{I + "/bus", []string{I + "/storage/storagetest"}},
		{I + "/cli", []string{I + "/storage/storagetest"}},
		{"github.com/quells-bot/hermes-chat/cmd/hermes-chat", []string{I + "/storage/storagetest"}},
		{"github.com/quells-bot/hermes-chat/cmd/hermes-chat", []string{I + "/storage/sqlcgen"}},
	}
	for _, c := range cases {
		assert.NotEmpty(t, checkLayering([]pkgImports{c}), "%s -> %v", c.ImportPath, c.Imports)
	}
}

func TestCheckLayering_Allowed(t *testing.T) {
	ok := []pkgImports{
		{I + "/app", []string{I + "/bus", S + "/reqid", "github.com/gin-gonic/gin", "net/http", S + "/sse"}},
		{I + "/bus", []string{I + "/storage", S + "/reqid"}},
		{I + "/storage", []string{I + "/storage/sqlcgen", S + "/reqid"}},
		{I + "/cli", []string{I + "/bus", I + "/storage"}},
		{"github.com/quells-bot/hermes-chat/cmd/hermes-chat", []string{I + "/app", I + "/bus", I + "/storage"}},
	}
	assert.Empty(t, checkLayering(ok))
}

// The real import graph, from `go list -json ./cmd/hermes-chat/...` run at the module root.
func TestLayering_ThisRepository(t *testing.T) {
	pkgs := listPackages(t)
	require.GreaterOrEqual(t, len(pkgs), 6)
	assert.Empty(t, checkLayering(pkgs))
}

// listPackages runs go list from the module root, whatever the test's working directory.
func listPackages(t *testing.T) []pkgImports {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Join(filepath.Dir(file), "..", "..")

	cmd := exec.Command("go", "list", "-json", "./cmd/hermes-chat/...")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run(), "go list failed: %s", stderr.String())

	var pkgs []pkgImports
	dec := json.NewDecoder(&stdout)
	for {
		var p pkgImports
		err := dec.Decode(&p)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		pkgs = append(pkgs, p)
	}
	return pkgs
}
