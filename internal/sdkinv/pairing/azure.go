package pairing

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const armPrefix = "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/"

var (
	azureLabelRe      = regexp.MustCompile(`^arm[a-z0-9]+:[A-Za-z0-9]+\.[A-Za-z0-9]+$`)
	azureLooseLabelRe = regexp.MustCompile(`^arm[a-z0-9]+:[A-Za-z0-9]+$`)
	azurePagerRe      = regexp.MustCompile(`^New(\w+)Pager$`)
	azureCtorRe       = regexp.MustCompile(`^New(\w*Client|ClientFactory)$`)
	azureTypeRe       = regexp.MustCompile(`^(\w*?Client)[A-Z]\w*$`) // <X>Client<Op>Response, ClientListOptions
)

type azureResolver struct{}

func init() { Register(azureResolver{}) }

func (azureResolver) Name() string                 { return "azure" }
func (azureResolver) LabelGrammar() *regexp.Regexp { return azureLabelRe }

// LooseLabelGrammar catches the near miss the strict grammar hides: an arm
// module and one bare segment, missing the Client. that 374 of the 376 live
// Azure labels carry. Without it "armdigitaltwins:List" was simply not a
// label, so nothing checked it.
func (azureResolver) LooseLabelGrammar() *regexp.Regexp { return azureLooseLabelRe }

// ImportKey: the arm module name (armcompute), majors stripped; fake and
// other sub-packages carry no clients.
func (azureResolver) ImportKey(path string) string {
	rest, ok := strings.CutPrefix(path, armPrefix)
	if !ok {
		return ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || !strings.HasPrefix(parts[1], "arm") {
		return ""
	}
	if len(parts) > 2 && !versionSegRe.MatchString("/"+parts[2]) {
		return ""
	}
	if len(parts) > 3 {
		return ""
	}
	return parts[1]
}

func (azureResolver) OpKey(op sdkinv.Operation) (string, string) {
	return op.Module[strings.LastIndex(op.Module, "/")+1:], op.Name
}

func (azureResolver) LabelAliases(_ sdkinv.Candidate, op sdkinv.Operation) []string {
	return []string{op.Label}
}

// LabelOp: labels drop the "Client" suffix of the client type
// ("armcompute:CloudServices.List" is CloudServicesClient.List); the module's
// base client is spelled "Client" in both.
func (azureResolver) LabelOp(lit string) string {
	_, op, _ := strings.Cut(lit, ":")
	client, method, ok := strings.Cut(op, ".")
	if !ok || client == "Client" {
		return op
	}
	return client + "Client." + method
}

func (azureResolver) TypeOwner(_, typeName string) (string, bool) {
	m := azureTypeRe.FindStringSubmatch(typeName)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func (azureResolver) Constructor(_, fn string) (string, bool) {
	m := azureCtorRe.FindStringSubmatch(fn)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Anchors: recv.New<Op>Pager( / recv.<Op>( where recv was built by
// armX.New<Y>Client or a client factory; an unbound receiver resolves when
// exactly one client of the imported modules has the op.
func (azureResolver) Anchors(f *Func) ([]Anchor, []Diagnostic) {
	var out []Anchor
	var diags []Diagnostic
	ast.Inspect(f.Decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		op := sel.Sel.Name
		paged := false
		if m := azurePagerRe.FindStringSubmatch(op); m != nil {
			op, paged = m[1], true
		}
		var b Binding
		bound := false
		recvName := ""
		switch recv := sel.X.(type) {
		case *ast.Ident:
			recvName = recv.Name
			b, bound = f.Vars[recv.Name]
		case *ast.SelectorExpr: // c.client.NewListPager(
			recvName = recv.Sel.Name
			b, bound = f.Fields[recv.Sel.Name]
		default:
			return true
		}
		if bound {
			// A pager or any List* on a bound client is an SDK call whether
			// or not the pinned SDK still has it (skew surfaces in Walk).
			if paged || f.Ops(b.Module, b.Ident+"."+op) || f.Other(b.Module, b.Ident+"."+op) || strings.HasPrefix(op, "List") {
				out = append(out, Anchor{Module: b.Module, Op: b.Ident + "." + op, Line: f.Line(call)})
			}
			return true
		}
		if _, isImport := f.Imports[recvName]; isImport || !paged {
			return true
		}
		var found []Anchor
		for _, mod := range f.SDKMods {
			for _, client := range f.ClientsWith(mod, op) {
				found = append(found, Anchor{Module: mod, Op: client + "." + op, Line: f.Line(call)})
			}
		}
		switch len(found) {
		case 1:
			out = append(out, found[0])
		case 0:
			diags = append(diags, Diagnostic{
				Kind: "unresolved-receiver", Line: f.Line(call),
				Message: fmt.Sprintf("%s.%s: receiver not bound to a client and no imported client has %s", recvName, sel.Sel.Name, op),
			})
		default:
			diags = append(diags, Diagnostic{
				Kind: "unresolved-receiver", Line: f.Line(call),
				Message: fmt.Sprintf("%s.%s: receiver not bound and %d clients have %s", recvName, sel.Sel.Name, len(found), op),
			})
		}
		return true
	})
	return out, diags
}
