package config

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// notReadOnPurpose lists config fields no code reads, with the reason. Keep it
// empty: a setting in the config must do what its comment says.
var notReadOnPurpose = map[string]string{}

// TestEverySettingIsReadSomewhere is a tripwire: every field of Config must be
// read by code outside this package. A setting that is documented, validated and
// imported but never read silently does nothing. The check is type-aware (a
// field of another type with the same name does not count) and counts reads
// only: the importer assigning a field, or the filter cycle overwriting one, is
// not a use. A read inside a Config method counts where the method is called.
func TestEverySettingIsReadSomewhere(t *testing.T) {
	const self = "github.com/luizfreitas27/aja/internal/config"
	pcfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax,
		Dir: "../..", Fset: token.NewFileSet()}
	pkgs, err := packages.Load(pcfg, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("the module does not type-check")
	}

	// Every leaf field of Config, by where it is declared.
	fields := map[string]string{} // position → yaml key
	var cfgType types.Type
	for _, p := range pkgs {
		if p.PkgPath == self {
			cfgType = p.Types.Scope().Lookup("Config").Type()
		}
	}
	if cfgType == nil {
		t.Fatal("Config not found")
	}
	var walkStruct func(typ types.Type, prefix string)
	walkStruct = func(typ types.Type, prefix string) {
		st := typ.Underlying().(*types.Struct)
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			name, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("yaml"), ",")
			key := prefix + name
			if _, ok := f.Type().Underlying().(*types.Struct); ok {
				walkStruct(f.Type(), key+".")
				continue
			}
			fields[pcfg.Fset.Position(f.Pos()).String()] = key
		}
	}
	walkStruct(cfgType, "")

	pos := func(o types.Object) string { return pcfg.Fset.Position(o.Pos()).String() }
	// readsIn lists the fields read in node, and the methods it calls.
	readsIn := func(node ast.Node, info *types.Info) (keys, calls []string) {
		written := map[*ast.SelectorExpr]bool{}
		ast.Inspect(node, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range s.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok {
						written[sel] = true
					}
				}
			case *ast.IncDecStmt:
				if sel, ok := s.X.(*ast.SelectorExpr); ok {
					written[sel] = true
				}
			}
			return true
		})
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || written[sel] {
				return true
			}
			if s, ok := info.Selections[sel]; ok {
				switch s.Kind() {
				case types.FieldVal:
					if key, ok := fields[pos(s.Obj())]; ok {
						keys = append(keys, key)
					}
				case types.MethodVal:
					calls = append(calls, pos(s.Obj()))
				}
			}
			return true
		})
		return keys, calls
	}

	// A method of Config that reads a field (cfg.AIBaseURL()) uses it for its
	// caller. Validate does not: validating a setting is not using it.
	viaMethod := map[string][]string{} // method position → fields it reads
	for _, p := range pkgs {
		if p.PkgPath != self {
			continue
		}
		for _, file := range p.Syntax {
			for _, d := range file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv == nil || fd.Body == nil || fd.Name.Name == "Validate" {
					continue
				}
				keys, _ := readsIn(fd.Body, p.TypesInfo)
				viaMethod[pos(p.TypesInfo.Defs[fd.Name])] = keys
			}
		}
	}

	read := map[string]bool{}
	for _, p := range pkgs {
		if p.PkgPath == self {
			continue
		}
		for _, file := range p.Syntax {
			keys, calls := readsIn(file, p.TypesInfo)
			for _, c := range calls {
				keys = append(keys, viaMethod[c]...)
			}
			for _, k := range keys {
				read[k] = true
			}
		}
	}

	var missing []string
	for _, key := range fields {
		if _, ok := notReadOnPurpose[key]; !read[key] && !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("settings nothing reads:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(read) < 10 {
		t.Errorf("only %d settings found read; the check itself is broken", len(read))
	}
}
