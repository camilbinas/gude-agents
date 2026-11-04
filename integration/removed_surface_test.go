package integration_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoGoFiles returns every Go source file in the repository, skipping vendored
// and scratch directories.
func repoGoFiles(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "tmp", ".worktrees", ".kiro":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found only %d Go files under %s; the scan is not looking at the repository", len(files), root)
	}
	return files
}

// Gude never chooses a model for the caller: the subjective tier aliases are
// gone from every provider package.
func TestNoTierAliasSymbolsRemain(t *testing.T) {
	banned := map[string]bool{"Cheapest": true, "Standard": true, "Smartest": true}
	fset := token.NewFileSet()
	for _, path := range repoGoFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && banned[fn.Name.Name] {
				t.Errorf("%s declares %s: tier aliases were removed", path, fn.Name.Name)
			}
		}
	}
}

// The provider registry and A2A modules were removed; nothing may import them.
func TestNoRegistryOrA2AImportsRemain(t *testing.T) {
	removed := []string{"/agent/a2a", "/agent/provider/registry"}
	fset := token.NewFileSet()
	for _, path := range repoGoFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, imp := range file.Imports {
			for _, bad := range removed {
				if strings.Contains(strings.Trim(imp.Path.Value, `"`), "gude-agents"+bad) {
					t.Errorf("%s imports %s", path, imp.Path.Value)
				}
			}
		}
	}
}

// Workspace and module files must not reference the removed modules either.
func TestNoRemovedModuleReferencesInModuleFiles(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "tmp", ".worktrees", ".kiro":
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "go.mod", "go.work":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, bad := range []string{"gude-agents/agent/a2a", "gude-agents/agent/provider/registry", "./agent/a2a", "./agent/provider/registry"} {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s still references %s", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// agent.Agent is now only the convenient constructor; the type is agent.Runtime.
// Go cannot declare a type and a function with the same name, so no alias can
// hide an old reference. This fails if any file uses agent.Agent as a type, or
// if package agent declares or refers to a type named Agent.
func TestAgentIsOnlyAConstructorNotAType(t *testing.T) {
	const agentImport = "github.com/camilbinas/gude-agents/agent"
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range repoGoFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		// Inside package agent itself, no type named Agent may exist or be used.
		if rel, err := filepath.Rel(root, path); err == nil && filepath.Dir(rel) == "agent" {
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.TypeSpec:
					if n.Name.Name == "Agent" {
						t.Errorf("%s: package agent declares type Agent; the type is Runtime", fset.Position(n.Pos()))
					}
				case *ast.StarExpr:
					if id, ok := n.X.(*ast.Ident); ok && id.Name == "Agent" {
						t.Errorf("%s: *Agent used as a type; use *Runtime", fset.Position(n.Pos()))
					}
				case *ast.CompositeLit:
					if id, ok := n.Type.(*ast.Ident); ok && id.Name == "Agent" {
						t.Errorf("%s: Agent{} literal; use Runtime{}", fset.Position(n.Pos()))
					}
				}
				return true
			})
			continue
		}

		// Everywhere else, agent.Agent may only be called, never used as a type.
		names := map[string]bool{}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) != agentImport {
				continue
			}
			if imp.Name != nil {
				names[imp.Name.Name] = true
			} else {
				names["agent"] = true
			}
		}
		if len(names) == 0 {
			continue
		}
		called := map[*ast.SelectorExpr]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					called[sel] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Agent" || called[sel] {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && names[id.Name] {
				t.Errorf("%s: agent.Agent used as a type; use agent.Runtime", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}
