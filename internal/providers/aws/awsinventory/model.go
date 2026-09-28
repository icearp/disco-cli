package awsinventory

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Smithy JSON AST subset.
type smithyModel struct {
	Shapes map[string]*shape `json:"shapes"`
}

type shape struct {
	Type    string                     `json:"type"`
	Traits  map[string]json.RawMessage `json:"traits"`
	Members map[string]*member         `json:"members"` // structure, union
	Member  *member                    `json:"member"`  // list
	Value   *member                    `json:"value"`   // map
	Input   *ref                       `json:"input"`   // operation
	Output  *ref                       `json:"output"`
	// Service and resource bindings.
	Operations           []ref          `json:"operations"`
	Resources            []ref          `json:"resources"`
	CollectionOperations []ref          `json:"collectionOperations"`
	Identifiers          map[string]ref `json:"identifiers"`
	Create               *ref           `json:"create"`
	Put                  *ref           `json:"put"`
	Read                 *ref           `json:"read"`
	Update               *ref           `json:"update"`
	Delete               *ref           `json:"delete"`
	List                 *ref           `json:"list"`
}

type member struct {
	Target string                     `json:"target"`
	Traits map[string]json.RawMessage `json:"traits"`
}

type ref struct {
	Target string `json:"target"`
}

// paginated is smithy.api#paginated. A service shape may carry defaults that
// every operation's own trait overrides member by member.
type paginated struct {
	InputToken  string `json:"inputToken"`
	OutputToken string `json:"outputToken"`
	Items       string `json:"items"`
	PageSize    string `json:"pageSize"`
}

// binding is how the service closure reaches an operation: through a
// resource's lifecycle ("list", "read", "create", …), as one of its instance
// or collection operations, or straight off the service (res == "").
type binding struct {
	res  string
	role string
}

// Binding roles beside the lifecycle names.
const (
	roleInstance   = "instance"
	roleCollection = "collection"
)

// resNode is one Smithy resource shape placed in the service's resource tree.
type resNode struct {
	id     string
	name   string // the local name without Smithy's conventional "Resource" suffix
	parent string // enclosing resource shape id, "" under the service
	depth  int
	all    []string // every identifier, sorted
	ids    []string // the identifiers this resource adds to its parent's
}

// serviceModel is one model file's service shape and everything it reaches.
type serviceModel struct {
	m          *smithyModel
	service    *shape
	pagDefault paginated
	ops        map[string]binding  // reachable operation shape id -> how it is bound
	res        map[string]*resNode // reachable resource shape id -> node
}

// newServiceModel walks the service shape's operations and resources. Only
// what the closure reaches is the service: healthlake.json carries a second,
// unreachable namespace whose ops the Go client does not have.
func newServiceModel(m *smithyModel) *serviceModel {
	sm := &serviceModel{m: m, ops: map[string]binding{}, res: map[string]*resNode{}}
	for _, id := range slices.Sorted(maps.Keys(m.Shapes)) {
		if m.Shapes[id].Type == "service" {
			sm.service = m.Shapes[id]
			break
		}
	}
	if sm.service == nil {
		return sm
	}
	decodeTrait(sm.service.Traits, "smithy.api#paginated", &sm.pagDefault)
	for _, o := range sm.service.Operations {
		sm.bind(o.Target, binding{})
	}
	for _, r := range sm.service.Resources {
		sm.walkResource(r.Target, "", 0)
	}
	sm.keepResourceSuffix()
	sm.nestByIdentifiers()
	return sm
}

// nestByIdentifiers places a resource the model declares at the service under
// the resource whose identifiers are the largest strict subset of its own:
// lambda declares FunctionAlias{FunctionName, Alias} beside
// Function{FunctionName}. Only named identifiers count (a bare "id" or
// "identifier" says nothing about which resource it is), and a tie nests
// nothing. The model's own nesting is never overridden.
// Then every node's depth follows its parent and its own ids are the ones its
// parent lacks.
func (sm *serviceModel) nestByIdentifiers() {
	ids := slices.Sorted(maps.Keys(sm.res))
	for _, id := range ids {
		n := sm.res[id]
		if n.parent != "" || len(n.all) < 2 {
			continue
		}
		best, tie := "", false
		for _, pid := range ids {
			p := sm.res[pid]
			if pid == id || len(p.all) == 0 || len(p.all) >= len(n.all) || !subset(p.all, n.all) || slices.ContainsFunc(p.all, func(i string) bool { return memberStem(i) == "" }) {
				continue
			}
			switch {
			case best == "" || len(p.all) > len(sm.res[best].all):
				best, tie = pid, false
			case len(p.all) == len(sm.res[best].all):
				tie = true
			}
		}
		if !tie {
			n.parent = best
		}
	}
	var depth func(n *resNode, seen int) int
	depth = func(n *resNode, seen int) int {
		p := sm.res[n.parent]
		if p == nil || seen > len(sm.res) {
			return 0
		}
		return depth(p, seen+1) + 1
	}
	for _, id := range ids {
		n := sm.res[id]
		n.depth = depth(n, 0)
		n.ids = nil
		var parentIDs []string
		if p := sm.res[n.parent]; p != nil {
			parentIDs = p.all
		}
		for _, i := range n.all {
			if !slices.Contains(parentIDs, i) {
				n.ids = append(n.ids, i)
			}
		}
	}
}

// keepResourceSuffix restores "Resource" on a name the model's operations
// spell with it: arc-zonal-shift's ManagedResource is listed by
// ListManagedResources, not a "Managed" resource.
func (sm *serviceModel) keepResourceSuffix() {
	for op, b := range sm.ops {
		n := sm.res[b.res]
		if n == nil {
			continue
		}
		full := shapeName(n.id)
		if full != n.name && sdkinv.Ident(opNoun(shapeName(op))) == sdkinv.Ident(full) {
			n.name = full
		}
	}
}

func subset(small, big []string) bool {
	for _, s := range small {
		if !slices.Contains(big, s) {
			return false
		}
	}
	return true
}

func (sm *serviceModel) walkResource(id, parent string, depth int) {
	r := sm.m.Shapes[id]
	if r == nil || r.Type != "resource" || sm.res[id] != nil {
		return
	}
	name := shapeName(id)
	if trimmed := strings.TrimSuffix(name, "Resource"); trimmed != "" {
		name = trimmed
	}
	sm.res[id] = &resNode{id: id, name: name, parent: parent, depth: depth, all: slices.Sorted(maps.Keys(r.Identifiers))}
	for _, lc := range []struct {
		role string
		op   *ref
	}{{"list", r.List}, {"read", r.Read}, {"create", r.Create}, {"put", r.Put}, {"update", r.Update}, {"delete", r.Delete}} {
		if lc.op != nil { // fixed order: an op bound twice keeps its first role
			sm.bind(lc.op.Target, binding{res: id, role: lc.role})
		}
	}
	for _, o := range r.Operations {
		sm.bind(o.Target, binding{res: id, role: roleInstance})
	}
	for _, o := range r.CollectionOperations {
		sm.bind(o.Target, binding{res: id, role: roleCollection})
	}
	for _, c := range r.Resources {
		sm.walkResource(c.Target, id, depth+1)
	}
}

// bind keeps a lifecycle binding over an instance or service one: a list op a
// resource names is that resource's listing wherever else it is reachable.
func (sm *serviceModel) bind(op string, b binding) {
	cur, ok := sm.ops[op]
	if !ok || cur.res == "" || (isLifecycle(b.role) && !isLifecycle(cur.role)) {
		sm.ops[op] = b
	}
}

func isLifecycle(role string) bool {
	return role != "" && role != roleInstance && role != roleCollection
}

// pagination is an operation's paginated trait merged over the service
// default; ok is false when neither is present.
func (sm *serviceModel) pagination(op *shape) (paginated, bool) {
	if _, ok := op.Traits["smithy.api#paginated"]; !ok {
		return paginated{}, false
	}
	p := sm.pagDefault
	var own paginated
	decodeTrait(op.Traits, "smithy.api#paginated", &own)
	for _, f := range []struct {
		dst *string
		v   string
	}{{&p.InputToken, own.InputToken}, {&p.OutputToken, own.OutputToken}, {&p.Items, own.Items}, {&p.PageSize, own.PageSize}} {
		if f.v != "" {
			*f.dst = f.v
		}
	}
	return p, true
}

// itemsElement resolves a paginated items path ("DistributionList.Items") on
// an output structure to the collection member's element shape id: a list's
// member or a map's value.
func (sm *serviceModel) itemsElement(out *shape, path string) string {
	cur := out
	segs := strings.Split(path, ".")
	for i, seg := range segs {
		mem := cur.Members[seg]
		if mem == nil {
			return ""
		}
		t := sm.m.Shapes[mem.Target]
		if t == nil {
			return ""
		}
		if i < len(segs)-1 {
			cur = t
			continue
		}
		switch {
		case t.Type == "list" && t.Member != nil:
			return t.Member.Target
		case t.Type == "map" && t.Value != nil:
			return t.Value.Target
		}
	}
	return ""
}

// httpTrait is smithy.api#http: the REST method and URI template.
type httpTrait struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}

// level is one labelled URI segment ("/fleets/{fleetId}"): the label and the
// static segment before it, "" when the label opens the path.
type level struct {
	label, seg string
}

// uriLevels lists the labels of an operation's URI outermost first, minus
// scope labels, and reports whether the path ends in a label.
func uriLevels(uri string) (levels []level, endsInLabel bool) {
	segs := sdkinv.ParseTemplate(uri)
	for i, s := range segs {
		if !s.Param || scopeParams[strings.ToLower(s.Text)] {
			continue
		}
		lv := level{label: s.Text}
		if i > 0 && !segs[i-1].Param {
			lv.seg = segs[i-1].Text
		}
		levels = append(levels, lv)
	}
	return levels, len(segs) > 0 && segs[len(segs)-1].Param
}

// decodeTrait unmarshals one trait into v, leaving v zero when it is absent
// or does not decode.
func decodeTrait(traits map[string]json.RawMessage, name string, v any) bool {
	raw, ok := traits[name]
	if !ok {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

// shapeName is a Smithy shape id's local name ("com.amazonaws.sqs#Queue" → "Queue").
func shapeName(id string) string { return id[strings.LastIndex(id, "#")+1:] }
