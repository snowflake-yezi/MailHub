package nodetransport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBusinessPackagesDoNotConstructNodeHTTPRequests(t *testing.T) {
	for _, packageName := range []string{"handler", "service", "healthcheck", "lifecycle"} {
		directory := filepath.Join("..", packageName)
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read %s: %v", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if strings.Contains(string(source), "http://") {
				t.Errorf("%s contains a direct node URL scheme; use NodeTransport", path)
			}
			assertNoHTTPRequestConstructors(t, path, source)
		}
	}
}

func assertNoHTTPRequestConstructors(t *testing.T, path string, source []byte) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	httpAliases := map[string]struct{}{}
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		if importPath != "net/http" {
			continue
		}
		name := "http"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		httpAliases[name] = struct{}{}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "NewRequest" && selector.Sel.Name != "NewRequestWithContext") {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, forbidden := httpAliases[identifier.Name]; forbidden {
			t.Errorf("%s constructs an HTTP request directly; use NodeTransport", path)
		}
		return true
	})
}
