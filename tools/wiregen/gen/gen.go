// Package gen is wiregen's extraction and emission library: the
// static-AST machinery behind `go run ./tools/wiregen`. It lives as an
// importable package (not inside main) on purpose — the freshness
// tests in wire/ call the SAME implementation the CLI runs, so the
// "twin extractor drifts from the generator" failure class is dead:
// there is exactly one extractor, and the tests execute it.
//
// Outputs (all committed artifacts, pinned fresh by tests):
//   - web/wire/gen/frame-contract.json  (frames + typed field shapes)
//   - web/wire/gen/types.js             (JSDoc full mirrors)
//   - <domain>/wireconv_gen.go          (mirror conversions, five pkgs)
package gen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Field is one struct field's contract shape: the Go name, the json
// tag (first segment, before any comma option) and the rendered Go
// type string ("string", "[]LogEntry", "*Proposal", ...). The type
// annotation is what the typedef emitter maps onto JSDoc; the wireconv
// emitter matches fields by tag and classifies by type.
type Field struct {
	Field string `json:"field"`
	Tag   string `json:"tag"`
	Type  string `json:"type,omitempty"`
}

// Contract is frame-contract.json's shape: the wire package's const
// vocabulary, every payload struct's typed field list, and the seated
// face's inbound decode shape (server's memberEnvelope).
type Contract struct {
	GeneratedBy string             `json:"generated_by"`
	Source      string             `json:"source"`
	Frames      map[string]string  `json:"frames"`
	Types       map[string][]Field `json:"types"`
	Envelope    []Field            `json:"envelope,omitempty"`
}

// renderType prints an ast.Expr back to its canonical source string.
func renderType(fset *token.FileSet, expr ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, expr); err != nil {
		return ""
	}
	return b.String()
}

// jsonTagOf pulls the first segment of the json tag ("name,omitempty"
// → "name"); empty when the field carries none.
func jsonTagOf(fl *ast.Field) string {
	if fl.Tag == nil {
		return ""
	}
	body := strings.Trim(fl.Tag.Value, "`")
	i := strings.Index(body, "json:\"")
	if i < 0 {
		return ""
	}
	rest := body[i+len("json:\""):]
	if j := strings.Index(rest, "\""); j >= 0 {
		return rest[:j]
	}
	return ""
}

// fieldsOf flattens one struct's fields (a shared-type declaration
// yields one Field per name, same tag and type).
func fieldsOf(fset *token.FileSet, st *ast.StructType) []Field {
	fields := []Field{}
	for _, fl := range st.Fields.List {
		tag := jsonTagOf(fl)
		typ := ""
		if fl.Type != nil {
			typ = renderType(fset, fl.Type)
		}
		for _, nm := range fl.Names {
			fields = append(fields, Field{Field: nm.Name, Tag: tag, Type: typ})
		}
	}
	return fields
}

// walkGoFiles parses every non-test .go file under dir (recursive —
// the server family may host memberEnvelope in a subpackage tomorrow)
// and feeds each file to visit.
func walkGoFiles(fset *token.FileSet, dir string, visit func(*ast.File) error) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		return visit(f)
	})
}

// ExtractWire pulls the wire package's const vocabulary and every
// struct's typed field shape — the contract's first half.
func ExtractWire(wireDir string) (frames map[string]string, types map[string][]Field, err error) {
	frames, types = map[string]string{}, map[string][]Field{}
	fset := token.NewFileSet()
	err = walkGoFiles(fset, wireDir, func(f *ast.File) error {
		ast.Inspect(f, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.GenDecl:
				if decl.Tok != token.CONST {
					return true
				}
				for _, spec := range decl.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue // grouped iota (none today)
						}
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							var val string
							_ = json.Unmarshal([]byte(lit.Value), &val)
							frames[name.Name] = val
						}
					}
				}
			case *ast.TypeSpec:
				if st, ok := decl.Type.(*ast.StructType); ok {
					types[decl.Name.Name] = fieldsOf(fset, st)
				}
			}
			return true
		})
		return nil
	})
	return frames, types, err
}

// ExtractEnvelope finds the seated face's inbound decode shape (the
// verbs package's Envelope, historically the shell's memberEnvelope)
// wherever it lives under the server family.
func ExtractEnvelope(serverDir string) ([]Field, error) {
	fset := token.NewFileSet()
	var envelope []Field
	err := walkGoFiles(fset, serverDir, func(f *ast.File) error {
		ast.Inspect(f, func(n ast.Node) bool {
			if decl, ok := n.(*ast.TypeSpec); ok && (decl.Name.Name == "memberEnvelope" || decl.Name.Name == "Envelope") {
				if st, ok := decl.Type.(*ast.StructType); ok {
					envelope = fieldsOf(fset, st)
				}
			}
			return true
		})
		return nil
	})
	return envelope, err
}

// BuildContract assembles the full contract from the repo root.
func BuildContract(root string) (*Contract, error) {
	frames, types, err := ExtractWire(filepath.Join(root, "wire"))
	if err != nil {
		return nil, fmt.Errorf("wire: %w", err)
	}
	envelope, err := ExtractEnvelope(filepath.Join(root, "server"))
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return &Contract{
		GeneratedBy: "go run ./tools/wiregen",
		Source:      "wire/*.go + server family Envelope (non-test)",
		Frames:      frames,
		Types:       types,
		Envelope:    envelope,
	}, nil
}

// RenderContractJSON marshals the contract exactly as committed
// (two-space indent, trailing newline).
func RenderContractJSON(c *Contract) ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// sortedKeys is the deterministic-output helper for emitters.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
