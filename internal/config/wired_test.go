package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// notReadOnPurpose lists config fields no code reads, with the reason. Keep it
// empty: a setting in the config must do what its comment says.
var notReadOnPurpose = map[string]string{}

// TestEverySettingIsReadSomewhere is a tripwire: every field of Config must be
// read (as a selector, e.g. cfg.Settings.RunNonStop) by code outside this
// package. A setting that is documented, validated and imported but never read
// silently does nothing.
func TestEverySettingIsReadSomewhere(t *testing.T) {
	used := map[string]bool{}
	fset := token.NewFileSet()
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "config" || d.Name() == "testdata") && path != root {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					used[sel.Sel.Name] = true
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var missing []string
	var walkStruct func(t reflect.Type, prefix string)
	walkStruct = func(typ reflect.Type, prefix string) {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			key := prefix + yamlName(f)
			if f.Type.Kind() == reflect.Struct {
				walkStruct(f.Type, key+".")
				continue
			}
			if !used[f.Name] {
				if _, ok := notReadOnPurpose[key]; !ok {
					missing = append(missing, key+" ("+f.Name+")")
				}
			}
		}
	}
	walkStruct(reflect.TypeOf(Config{}), "")
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("settings nothing reads:\n  %s", strings.Join(missing, "\n  "))
	}
}
