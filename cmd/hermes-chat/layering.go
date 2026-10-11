package main

import (
	"fmt"
	"strings"
)

const internalRoot = "github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal"

// pkgImports is one package and its non-test imports, as reported by go list.
type pkgImports struct {
	ImportPath string
	Imports    []string // non-test imports only
}

// forbidden maps a package to the packages it must not import. Each entry
// covers the named package and everything under it.
var forbidden = map[string][]string{
	internalRoot + "/app":     {internalRoot + "/storage"},
	internalRoot + "/bus":     {internalRoot + "/app", internalRoot + "/storage/sqlcgen", "github.com/gin-gonic/gin", "net/http"},
	internalRoot + "/storage": {internalRoot + "/bus", internalRoot + "/app", "github.com/gin-gonic/gin"},
	internalRoot + "/reqid":   {internalRoot + "/app", internalRoot + "/bus", internalRoot + "/storage"},
	internalRoot + "/sse":     {internalRoot + "/app", internalRoot + "/bus", internalRoot + "/storage"},
}

// within reports whether path is root or lies under it.
func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// checkLayering returns one message per forbidden import; empty means the rules hold.
func checkLayering(pkgs []pkgImports) []string {
	var out []string
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			for owner, banned := range forbidden {
				if !within(p.ImportPath, owner) {
					continue
				}
				for _, b := range banned {
					if within(imp, b) {
						out = append(out, fmt.Sprintf("%s must not import %s", p.ImportPath, imp))
					}
				}
			}
			if within(imp, internalRoot+"/storage/storagetest") {
				out = append(out, fmt.Sprintf("%s must not import %s (test helper, imports testing)", p.ImportPath, imp))
			}
			sqlcgen := internalRoot + "/storage/sqlcgen"
			if within(imp, sqlcgen) && !within(p.ImportPath, internalRoot+"/storage") {
				out = append(out, fmt.Sprintf("%s must not import %s (only storage may)", p.ImportPath, imp))
			}
		}
	}
	return out
}
