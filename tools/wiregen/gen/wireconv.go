// wireconv.go — the mirror-conversion emitter: generates the two-way
// bridge between each domain payload and its wire mirror (the code
// that used to be hand-written in five <domain>/wireconv.go files).
//
// The generation rule is the discipline, made mechanical: fields match
// by json tag; a wire field with no domain counterpart is a MIRROR
// LIE (generation fails — the mirror claims protocol the domain
// cannot honor); a domain field with no wire counterpart is a
// deliberate off-wire field (listed in the generated header — the
// protocol decision surface, visible in every diff). Conversions for
// nested mirror pairs reuse the pair's own generated methods, so the
// whole family regenerates as one consistent unit.
package gen

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// Pair is one domain-type ↔ wire-mirror conversion pair. Method
// (false for the flat inline-only shapes — LogEntry/PlanTask/Line —
// which never get methods, matching the hand-written API surface);
// ValueRecv marks a value-receiver Wire() (Patch); ZeroFrom marks
// FromWire(nil) returning the zero value instead of nil (Patch).
type Pair struct {
	PkgDir    string // domain package directory ("tasks")
	Dom, Wire string // type names (identical by convention)
	Method    bool
	ValueRecv bool
	ZeroFrom  bool
}

// Pairs is the registry. Order matters only for output layout.
var Pairs = []Pair{
	{PkgDir: "tasks", Dom: "Task", Wire: "Task", Method: true},
	{PkgDir: "tasks", Dom: "Patch", Wire: "Patch", Method: true, ValueRecv: true, ZeroFrom: true},
	{PkgDir: "tasks", Dom: "Proposal", Wire: "Proposal", Method: true},
	{PkgDir: "tasks", Dom: "LogEntry", Wire: "LogEntry"},
	{PkgDir: "plan", Dom: "Plan", Wire: "Plan", Method: true},
	{PkgDir: "plan", Dom: "PlanTask", Wire: "PlanTask"},
	{PkgDir: "merge", Dom: "Merge", Wire: "Merge", Method: true},
	{PkgDir: "requirements", Dom: "Req", Wire: "Req", Method: true},
	{PkgDir: "meeting", Dom: "Meeting", Wire: "Meeting", Method: true},
	{PkgDir: "meeting", Dom: "Line", Wire: "Line"},
}

// recvName picks the historical single-letter receiver.
func recvName(typeName string) string {
	if typeName == "" {
		return "x"
	}
	return strings.ToLower(typeName[:1])
}

// fromName is the inverse-converter's name (Task → TaskFromWire).
func fromName(typeName string) string { return typeName + "FromWire" }

// structShape is one parsed struct: its fields keyed by first json
// tag segment, in declaration order.
type structShape struct {
	byTag map[string]Field
	order []string
}

// parseStructs parses a package directory's non-test *.go files and
// returns the wanted structs.
func parseStructs(pkgDir string, want map[string]bool) (map[string]structShape, error) {
	fset := token.NewFileSet()
	out := map[string]structShape{}
	entries, err := filepath.Glob(filepath.Join(pkgDir, "*.go"))
	if err != nil {
		return nil, err
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil, perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			decl, ok := n.(*ast.TypeSpec)
			if !ok || !want[decl.Name.Name] {
				return true
			}
			st, ok := decl.Type.(*ast.StructType)
			if !ok {
				return true
			}
			shape := structShape{byTag: map[string]Field{}}
			for _, fl := range fieldsOf(fset, st) {
				if fl.Tag == "" {
					continue
				}
				key := strings.SplitN(fl.Tag, ",", 2)[0]
				if _, dup := shape.byTag[key]; !dup {
					shape.order = append(shape.order, key)
				}
				shape.byTag[key] = fl
			}
			out[decl.Name.Name] = shape
			return true
		})
	}
	return out, nil
}

// normWireType strips the wire package qualifier ("wire.Task" → "Task").
func normWireType(s string) string { return strings.TrimPrefix(s, "wire.") }

// kind names the conversion shape of one tag-matched field pair.
const (
	kindScalar    = "scalar"     // same shape both sides: direct copy
	kindPairValue = "pair-value" // Dom value ↔ wire value: method call
	kindPairPtr   = "pair-ptr"   // *Dom ↔ *wire: nil-guarded method call
	kindFlatSlice = "flat-slice" // []Dom ↔ []wire of a flat pair: len-guarded loop
)

// classify resolves how one tag-matched field converts. pairByDom maps
// the domain type name (bare, element or pointee) to its pair.
func classify(domType, wireType string, pairByDom map[string]Pair) (string, Pair, error) {
	domElem := strings.TrimPrefix(strings.TrimPrefix(domType, "*"), "[]")
	if p, ok := pairByDom[domElem]; ok {
		wireElem := strings.TrimPrefix(strings.TrimPrefix(normWireType(wireType), "*"), "[]")
		wireElem = strings.TrimPrefix(strings.TrimPrefix(wireElem, "[]"), "*")
		if wireElem == p.Wire {
			switch {
			case domType == "[]"+domElem:
				return kindFlatSlice, p, nil
			case strings.HasPrefix(domType, "*"):
				return kindPairPtr, p, nil
			default:
				return kindPairValue, p, nil
			}
		}
	}
	if domType == normWireType(wireType) {
		return kindScalar, Pair{}, nil
	}
	return "", Pair{}, fmt.Errorf("字段形状无法对齐：域 %s ↔ 镜像 %s（既非标量也非配对形状）", domType, wireType)
}

// classifyAll pairs every mirrored tag of one pair; a wire tag without
// a domain counterpart is the mirror-lie fatal error.
func classifyAll(p Pair, dom, mir structShape, pairByDom map[string]Pair) (map[string]string, map[string]Pair, error) {
	kinds, kindPairs := map[string]string{}, map[string]Pair{}
	for _, tag := range mir.order {
		df, ok := dom.byTag[tag]
		if !ok {
			return nil, nil, fmt.Errorf("%s.%s：wire 镜像有 json:%s 而域类型没有——镜像谎言，先补域侧或删镜像字段", p.PkgDir, p.Dom, tag)
		}
		kind, pp, err := classify(df.Type, mir.byTag[tag].Type, pairByDom)
		if err != nil {
			return nil, nil, fmt.Errorf("%s.%s（json:%s）: %w", p.Dom, df.Field, tag, err)
		}
		kinds[tag] = kind
		kindPairs[tag] = pp
	}
	return kinds, kindPairs, nil
}

// GenWireconv renders every pair's conversions, grouped per domain
// package, as wireconv_gen.go contents keyed by output path.
func GenWireconv(root string) (map[string][]byte, error) {
	pairByDom := map[string]Pair{}
	for _, p := range Pairs {
		pairByDom[p.Dom] = p
	}
	wantWire := map[string]bool{}
	for _, p := range Pairs {
		wantWire[p.Wire] = true
	}
	wireShapes, err := parseStructs(filepath.Join(root, "wire"), wantWire)
	if err != nil {
		return nil, err
	}

	// Pre-pass: flat pairs' scalar field lists (matched by tag, every
	// field required scalar on both sides — the inline-literal contract).
	flat := map[string][]Field{}
	for _, p := range Pairs {
		if p.Method {
			continue
		}
		domShapes, err := parseStructs(filepath.Join(root, p.PkgDir), map[string]bool{p.Dom: true})
		if err != nil {
			return nil, err
		}
		dom, ok := domShapes[p.Dom]
		if !ok {
			return nil, fmt.Errorf("%s: 域类型 %s 未找到", p.PkgDir, p.Dom)
		}
		mir, ok := wireShapes[p.Wire]
		if !ok {
			return nil, fmt.Errorf("wire: 镜像类型 %s 未找到", p.Wire)
		}
		var fs []Field
		for _, tag := range mir.order {
			df := dom.byTag[tag]
			if df.Type != normWireType(mir.byTag[tag].Type) {
				return nil, fmt.Errorf("%s.%s（json:%s）：内联平对字段必须是两侧同形标量（域 %s ↔ 镜像 %s）", p.Dom, df.Field, tag, df.Type, mir.byTag[tag].Type)
			}
			fs = append(fs, df)
		}
		flat[p.Dom] = fs
	}

	out := map[string][]byte{}
	perPkg := map[string][]Pair{}
	for _, p := range Pairs {
		perPkg[p.PkgDir] = append(perPkg[p.PkgDir], p)
	}
	for _, pkgDir := range sortedKeys(perPkg) {
		pairs := perPkg[pkgDir]
		wantDom := map[string]bool{}
		for _, p := range pairs {
			wantDom[p.Dom] = true
		}
		domShapes, err := parseStructs(filepath.Join(root, pkgDir), wantDom)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pkgDir, err)
		}
		pkgName := filepath.Base(pkgDir)
		var b strings.Builder
		b.WriteString("// Code generated by tools/wiregen; DO NOT EDIT.\n")
		fmt.Fprintf(&b, "// wireconv_gen.go — %s ↔ wire 镜像的双向转换，由 `go run ./tools/wiregen`\n", pkgName)
		b.WriteString("// 从两侧 struct 形状（json tag 配对）生成。手写面只剩非机械 helper（若有，\n")
		b.WriteString("// 见同目录 wireconv.go）。生成规则即纪律：wire 有域无＝镜像谎言（生成期\n")
		b.WriteString("// 报错）；域有 wire 无＝刻意不进协议（文件尾清单成文）。改字段流程：\n")
		b.WriteString("// 域＋wire 镜像＋fixture 三处手写，跑一次 wiregen，其余全生成。\n\n")
		fmt.Fprintf(&b, "package %s\n\n", pkgName)
		b.WriteString("import \"github.com/WWestC/Niuma_Studio/wire\"\n")

		var ignored []string
		for _, p := range pairs {
			dom, mir := domShapes[p.Dom], wireShapes[p.Wire]
			if _, ok := domShapes[p.Dom]; !ok {
				return nil, fmt.Errorf("%s: 域类型 %s 未找到", pkgDir, p.Dom)
			}
			if _, ok := wireShapes[p.Wire]; !ok {
				return nil, fmt.Errorf("wire: 镜像类型 %s 未找到", p.Wire)
			}
			kinds, kindPairs, err := classifyAll(p, dom, mir, pairByDom)
			if err != nil {
				return nil, err
			}
			for _, tag := range dom.order {
				if _, ok := mir.byTag[tag]; !ok {
					ignored = append(ignored, p.Dom+"."+dom.byTag[tag].Field+"（json:"+tag+"）")
				}
			}
			if !p.Method {
				continue
			}
			emitWire(&b, p, dom, mir, kinds, kindPairs, flat)
			emitFromWire(&b, p, dom, mir, kinds, kindPairs, flat)
		}
		if len(ignored) > 0 {
			b.WriteString("\n// 刻意不进协议的域字段（wire 镜像无此 tag；加进协议＝显式改镜像＋两侧对齐）：\n")
			for _, s := range ignored {
				fmt.Fprintf(&b, "//   %s\n", s)
			}
		}
		// 生成物不豁免 CI 的 gofmt 检查（规模护栏豁免、格式不豁免）：
		// 落盘前过 format.Source，模板手拼的对齐交给官方格式器定稿。
		// 新鲜度测试进程内重跑的正是这个函数——两侧字节天然一致。
		src, err := format.Source([]byte(b.String()))
		if err != nil {
			return nil, fmt.Errorf("%s: 生成的 Go 不合法（wiregen 模板 bug）: %w", pkgDir, err)
		}
		out[filepath.Join(pkgDir, "wireconv_gen.go")] = src
	}
	return out, nil
}

// emitWire writes the Wire() projection: scalars and value-pairs in
// the struct literal, flat slices and pointer pairs as guarded blocks
// after it (absent stays absent — nil and empty are different wire
// states, the hand-written invariant).
func emitWire(b *strings.Builder, p Pair, dom, mir structShape, kinds map[string]string, kindPairs map[string]Pair, flat map[string][]Field) {
	r := recvName(p.Dom)
	b.WriteString("\n")
	fmt.Fprintf(b, "// Wire projects the %s onto its protocol mirror.\n", strings.ToLower(p.Dom))
	if p.ValueRecv {
		fmt.Fprintf(b, "func (%s %s) Wire() wire.%s {\n", r, p.Dom, p.Wire)
		fmt.Fprintf(b, "\treturn wire.%s{\n", p.Wire)
	} else {
		fmt.Fprintf(b, "func (%s *%s) Wire() *wire.%s {\n", r, p.Dom, p.Wire)
		fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn nil\n\t}\n", r)
		fmt.Fprintf(b, "\tout := wire.%s{\n", p.Wire)
	}
	for _, tag := range mir.order {
		f := dom.byTag[tag]
		switch kinds[tag] {
		case kindScalar:
			fmt.Fprintf(b, "\t\t%s: %s.%s,\n", f.Field, r, f.Field)
		case kindPairValue:
			fmt.Fprintf(b, "\t\t%s: %s.%s.Wire(),\n", f.Field, r, f.Field)
		}
	}
	if p.ValueRecv {
		b.WriteString("\t}\n}\n")
		return
	}
	b.WriteString("\t}\n")
	for _, tag := range mir.order {
		f := dom.byTag[tag]
		switch kinds[tag] {
		case kindFlatSlice:
			pp := kindPairs[tag]
			fmt.Fprintf(b, "\tif len(%s.%s) > 0 {\n", r, f.Field)
			fmt.Fprintf(b, "\t\tout.%s = make([]wire.%s, len(%s.%s))\n", f.Field, pp.Wire, r, f.Field)
			fmt.Fprintf(b, "\t\tfor i, e := range %s.%s {\n", r, f.Field)
			fmt.Fprintf(b, "\t\t\tout.%s[i] = wire.%s{\n", f.Field, pp.Wire)
			for _, ff := range flat[pp.Dom] {
				fmt.Fprintf(b, "\t\t\t\t%s: e.%s,\n", ff.Field, ff.Field)
			}
			b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
		case kindPairPtr:
			fmt.Fprintf(b, "\tif %s.%s != nil {\n", r, f.Field)
			fmt.Fprintf(b, "\t\tout.%s = %s.%s.Wire()\n", f.Field, r, f.Field)
			b.WriteString("\t}\n")
		}
	}
	_ = p.Wire
	b.WriteString("\treturn &out\n}\n")
}

// emitFromWire writes the inverse: same shape classification, nil at
// the door (nil frame payload → nil domain pointer, or the zero value
// for the ZeroFrom pair).
func emitFromWire(b *strings.Builder, p Pair, dom, mir structShape, kinds map[string]string, kindPairs map[string]Pair, flat map[string][]Field) {
	w := "w"
	fmt.Fprintf(b, "\n// %s is Wire's inverse (nil-safe: a nil frame payload stays nil —\n// \"absent\" and \"empty\" are different wire states).\n", fromName(p.Dom))
	fmt.Fprintf(b, "func %s(%s *wire.%s) ", fromName(p.Dom), w, p.Wire)
	if p.ZeroFrom {
		fmt.Fprintf(b, "%s {\n", p.Dom)
		fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn %s{}\n\t}\n", w, p.Dom)
		fmt.Fprintf(b, "\treturn %s{\n", p.Dom)
	} else {
		fmt.Fprintf(b, "*%s {\n", p.Dom)
		fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn nil\n\t}\n", w)
		fmt.Fprintf(b, "\tout := %s{\n", p.Dom)
	}
	for _, tag := range mir.order {
		f := dom.byTag[tag]
		switch kinds[tag] {
		case kindScalar:
			fmt.Fprintf(b, "\t\t%s: %s.%s,\n", f.Field, w, f.Field)
		case kindPairValue:
			fmt.Fprintf(b, "\t\t%s: %s(&%s.%s),\n", f.Field, fromName(kindPairs[tag].Dom), w, f.Field)
		}
	}
	if p.ZeroFrom {
		b.WriteString("\t}\n}\n")
		return
	}
	b.WriteString("\t}\n")
	for _, tag := range mir.order {
		f := dom.byTag[tag]
		switch kinds[tag] {
		case kindFlatSlice:
			pp := kindPairs[tag]
			fmt.Fprintf(b, "\tif len(%s.%s) > 0 {\n", w, f.Field)
			fmt.Fprintf(b, "\t\tout.%s = make([]%s, len(%s.%s))\n", f.Field, pp.Dom, w, f.Field)
			fmt.Fprintf(b, "\t\tfor i, e := range %s.%s {\n", w, f.Field)
			fmt.Fprintf(b, "\t\t\tout.%s[i] = %s{\n", f.Field, pp.Dom)
			for _, ff := range flat[pp.Dom] {
				fmt.Fprintf(b, "\t\t\t\t%s: e.%s,\n", ff.Field, ff.Field)
			}
			b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
		case kindPairPtr:
			fmt.Fprintf(b, "\tout.%s = %s(%s.%s)\n", f.Field, fromName(kindPairs[tag].Dom), w, f.Field)
		}
	}
	b.WriteString("\treturn &out\n}\n")
}
