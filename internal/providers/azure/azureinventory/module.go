package azureinventory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// module is what one arm* module's generated source states, read from its
// syntax tree: the request each client method sends, the model its response
// decodes into, and each model field's JSON name.
type module struct {
	path     string               // "<rp>/arm<mod>"
	structs  map[string][]field   // model name -> fields in declaration order
	bases    map[string]string    // "XClassification" -> "X", from its GetX() *X method
	wire     map[string]wireNames // model -> Go field -> JSON name
	builders []builder
	source   []sdkinv.OpRef
	drops    []sdkinv.Drop
}

type field struct {
	name, typ string // typ as written: "*string", "[]*Widget", "WidgetListResult" (embedded)
}

type wireNames map[string]string

// builder is one request builder: a client method returning *policy.Request
// whose body sets a literal urlPath and HTTP method.
type builder struct {
	client string // receiver type: "<X>Client", or the bare "Client"
	op     string // the builder's name less "CreateRequest", upper-first
	method string // GET, PUT, ...
	path   string
	module string
	paged  bool   // an exported method returning *runtime.Pager reaches it
	result string // type of the field the response decoder fills (&result.<F>), when one is reached
}

func parseModule(root, rel string) (*module, error) {
	dir := filepath.Join(root, filepath.FromSlash(rel))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	m := &module{path: rel, structs: map[string][]field{}, bases: map[string]string{}, wire: map[string]wireNames{}}
	methods := map[string]map[string]*ast.FuncDecl{} // receiver type -> method name -> decl
	fset := token.NewFileSet()
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				m.addTypes(d)
			case *ast.FuncDecl:
				if recv := receiverType(d); recv != "" {
					if methods[recv] == nil {
						methods[recv] = map[string]*ast.FuncDecl{}
					}
					methods[recv][d.Name.Name] = d
				}
			}
		}
	}
	recvs := make([]string, 0, len(methods))
	for r := range methods {
		recvs = append(recvs, r)
	}
	sort.Strings(recvs)
	for _, recv := range recvs {
		m.addWire(recv, methods[recv])
		m.addClient(recv, methods[recv])
	}
	return m, nil
}

func (m *module) addTypes(d *ast.GenDecl) {
	if d.Tok != token.TYPE {
		return
	}
	for _, s := range d.Specs {
		ts := s.(*ast.TypeSpec)
		switch t := ts.Type.(type) {
		case *ast.StructType:
			var fields []field
			for _, f := range t.Fields.List {
				typ := exprString(f.Type)
				if len(f.Names) == 0 {
					fields = append(fields, field{name: strings.TrimPrefix(typ, "*"), typ: typ})
				}
				for _, n := range f.Names {
					fields = append(fields, field{name: n.Name, typ: typ})
					if json := jsonTag(f.Tag); json != "" {
						m.setWire(ts.Name.Name, n.Name, json)
					}
				}
			}
			m.structs[ts.Name.Name] = fields
		case *ast.InterfaceType:
			// A polymorphic model is an interface whose one method returns the
			// common base: "GetWidget() *Widget".
			if len(t.Methods.List) != 1 {
				continue
			}
			if fn, ok := t.Methods.List[0].Type.(*ast.FuncType); ok && fn.Results != nil && len(fn.Results.List) == 1 {
				if base, ok := strings.CutPrefix(exprString(fn.Results.List[0].Type), "*"); ok {
					m.bases[ts.Name.Name] = base
				}
			}
		}
	}
}

// jsonTag is the older generator's `json:"name,omitempty"` field name.
func jsonTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(tag.Value)
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(raw, `json:"`)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, `"`)
	name, _, _ = strings.Cut(name, ",")
	if name == "-" {
		return ""
	}
	return name
}

func (m *module) setWire(model, goName, json string) {
	if m.wire[model] == nil {
		m.wire[model] = wireNames{}
	}
	if _, set := m.wire[model][goName]; !set {
		m.wire[model][goName] = json
	}
}

// addWire reads a model's JSON names from its serde methods. MarshalJSON
// writes each field as populate(objectMap, "<json>", x.<Field>); UnmarshalJSON
// switches on the JSON name and assigns &x.<Field>. Both are read because
// either may omit a field the other carries.
func (m *module) addWire(model string, methods map[string]*ast.FuncDecl) {
	if d := methods["MarshalJSON"]; d != nil && d.Body != nil {
		recv := receiverName(d)
		ast.Inspect(d.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var json, goName string
			for _, a := range call.Args {
				if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING && json == "" {
					json, _ = strconv.Unquote(lit.Value)
				}
				if f := receiverField(a, recv); f != "" {
					goName = f
				}
			}
			if json != "" && goName != "" {
				m.setWire(model, goName, json)
			}
			return true
		})
	}
	if d := methods["UnmarshalJSON"]; d != nil && d.Body != nil {
		recv := receiverName(d)
		ast.Inspect(d.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok || len(cc.List) != 1 {
				return true
			}
			lit, ok := cc.List[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			json, _ := strconv.Unquote(lit.Value)
			for _, st := range cc.Body {
				ast.Inspect(st, func(n ast.Node) bool {
					if u, ok := n.(*ast.UnaryExpr); ok && u.Op == token.AND {
						if f := receiverField(u.X, recv); f != "" {
							m.setWire(model, f, json)
						}
					}
					return true
				})
			}
			return true
		})
	}
}

// addClient records a client's request builders and accounts for its
// exported methods. An exported method is followed through every client.x(...)
// call it makes, transitively, to the builder it sends and the decoder it
// reads the response with; nothing depends on the wrapper names (Begin<Op>,
// New<Op>Pager) the generator uses.
func (m *module) addClient(recv string, methods map[string]*ast.FuncDecl) {
	builders := map[string]*builder{} // method name -> builder
	unparsed := map[string]bool{}     // builders with no literal path or verb
	for name, d := range methods {
		if !returnsPolicyRequest(d) {
			continue
		}
		path, verb := requestOf(d)
		op := sdkinv.UpperFirst(strings.TrimSuffix(name, "CreateRequest"))
		if path == "" || verb == "" {
			unparsed[name] = true
			builders[name] = &builder{client: recv, op: op, module: m.path}
			continue
		}
		builders[name] = &builder{client: recv, op: op, method: verb, path: path, module: m.path}
	}
	if len(builders) == 0 {
		return
	}
	names := make([]string, 0, len(methods))
	for n := range methods {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		d := methods[name]
		if !ast.IsExported(name) || d.Body == nil {
			continue
		}
		reached := reach(d, methods)
		var hit []string
		var result string
		for _, r := range reached {
			if builders[r] != nil {
				hit = append(hit, r)
			}
			if typ := decodedType(methods[r], m.structs); typ != "" && result == "" {
				result = typ
			}
		}
		if len(hit) == 0 {
			b := builder{client: recv, op: name, module: m.path}
			if !seen[name] {
				seen[name] = true
				op := opFor(b, "", nil, "")
				m.source = append(m.source, op.Ref())
				m.drops = append(m.drops, sdkinv.Drop{Op: op, Reason: "no-request-builder"})
			}
			continue
		}
		paged := returnsPager(d)
		for _, h := range hit {
			b := builders[h]
			b.paged = b.paged || paged
			if b.result == "" {
				b.result = result
			}
			if seen[h] {
				continue
			}
			seen[h] = true
			op := opFor(*b, "", nil, "")
			m.source = append(m.source, op.Ref())
			if unparsed[h] {
				m.drops = append(m.drops, sdkinv.Drop{Op: op, Reason: "no-request-path"})
			}
		}
	}
	bnames := make([]string, 0, len(builders))
	for n := range builders {
		bnames = append(bnames, n)
	}
	sort.Strings(bnames)
	for _, n := range bnames {
		if !unparsed[n] {
			m.builders = append(m.builders, *builders[n])
		}
	}
}

// reach lists the client methods d calls on its own receiver, transitively,
// in first-seen order.
func reach(d *ast.FuncDecl, methods map[string]*ast.FuncDecl) []string {
	var out []string
	seen := map[string]bool{d.Name.Name: true}
	queue := []*ast.FuncDecl{d}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		recv := receiverName(cur)
		ast.Inspect(cur.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != recv {
				return true
			}
			name := sel.Sel.Name
			if next := methods[name]; next != nil && !seen[name] && next.Body != nil {
				seen[name] = true
				out = append(out, name)
				queue = append(queue, next)
			}
			return true
		})
	}
	return out
}

// requestOf reads a builder's literal urlPath and http.Method<Verb>.
func requestOf(d *ast.FuncDecl) (path, verb string) {
	if d.Body == nil {
		return "", ""
	}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == 1 && len(n.Rhs) == 1 {
				if id, ok := n.Lhs[0].(*ast.Ident); ok && id.Name == "urlPath" && path == "" {
					if lit, ok := n.Rhs[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						path, _ = strconv.Unquote(lit.Value)
					}
				}
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && id.Name == "http" && verb == "" {
				if v, ok := strings.CutPrefix(n.Sel.Name, "Method"); ok {
					verb = strings.ToUpper(v)
				}
			}
		}
		return true
	})
	return path, verb
}

// decodedType is the type of the field a response decoder unmarshals into
// (&result.<F>), looked up on the decoder's first result type.
func decodedType(d *ast.FuncDecl, structs map[string][]field) string {
	if d == nil || d.Body == nil || d.Type.Results == nil || len(d.Type.Results.List) == 0 {
		return ""
	}
	resp := exprString(d.Type.Results.List[0].Type)
	var name string
	ast.Inspect(d.Body, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND || name != "" {
			return name == ""
		}
		if sel, ok := u.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "result" {
				name = sel.Sel.Name
			}
		}
		return true
	})
	if name == "" {
		return ""
	}
	for _, f := range structs[resp] {
		if f.name == name {
			return f.typ
		}
	}
	return ""
}

func returnsPolicyRequest(d *ast.FuncDecl) bool {
	return d.Type.Results != nil && slices.ContainsFunc(d.Type.Results.List, func(f *ast.Field) bool {
		return exprString(f.Type) == "*policy.Request"
	})
}

func returnsPager(d *ast.FuncDecl) bool {
	return d.Type.Results != nil && len(d.Type.Results.List) > 0 &&
		strings.HasPrefix(exprString(d.Type.Results.List[0].Type), "*runtime.Pager[")
}

func receiverType(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	return strings.TrimPrefix(exprString(d.Recv.List[0].Type), "*")
}

func receiverName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 || len(d.Recv.List[0].Names) == 0 {
		return ""
	}
	return d.Recv.List[0].Names[0].Name
}

// receiverField names the field in "recv.F" or "&recv.F".
func receiverField(e ast.Expr, recv string) string {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == recv {
		return sel.Sel.Name
	}
	return ""
}

// exprString prints the type expressions generated models use.
func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.ArrayType:
		if e.Len != nil {
			return "[" + exprString(e.Len) + "]" + exprString(e.Elt)
		}
		return "[]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.IndexExpr:
		return exprString(e.X) + "[" + exprString(e.Index) + "]"
	case *ast.BasicLit:
		return e.Value
	case *ast.InterfaceType:
		return "any"
	}
	return "?"
}
