package azureinventory

import (
	"slices"
	"sort"
	"strings"
)

// shape is what a response decodes into: a list of element models, or one
// model.
type shape struct {
	element string // the listed model, list only
	model   string // the one model, single only
	list    bool
}

// shapeOf reads a decoded field's type. A slice is a list of its element. A
// model with exactly one slice of models and no ID of its own is a list
// result wrapping that slice, whatever the field is named (Value, Items,
// RolloutArray) and whatever else it carries (attestation's list result
// holds SystemData, billing's a Summary). Among several slices, the one
// whose elements carry an ID is the list (billingbenefits' savings plans
// beside their summaries). Any other model is one model:
// VirtualMachineInstanceView holds several slices of unidentified views,
// hybridcompute's validation details an ID of their own.
func (m *module) shapeOf(typ string) shape {
	if elt, ok := strings.CutPrefix(typ, "[]"); ok {
		return shape{element: m.model(elt), list: true}
	}
	name := m.model(typ)
	if name == "" {
		return shape{}
	}
	var lists []string
	for _, f := range m.structs[name] {
		if f.name == "ID" {
			return shape{model: name}
		}
		if elt, ok := strings.CutPrefix(f.typ, "[]"); ok && m.model(elt) != "" {
			lists = append(lists, m.model(elt))
		}
	}
	if len(lists) > 1 {
		lists = slicesWithID(m, lists)
	}
	if len(lists) == 1 {
		return shape{element: lists[0], list: true}
	}
	return shape{model: name}
}

func slicesWithID(m *module, elements []string) []string {
	var out []string
	for _, el := range elements {
		for _, f := range m.structs[el] {
			if f.name == "ID" {
				out = append(out, el)
				break
			}
		}
	}
	return out
}

// model resolves a field type to the model it holds: "*Widget" is Widget,
// and a polymorphic "WidgetClassification" is its base Widget. A type that
// is not a model of this module (string, a map, an azcore type) is "".
func (m *module) model(typ string) string {
	name := strings.TrimLeft(typ, "*")
	if base, ok := m.bases[name]; ok {
		name = base
	}
	if _, ok := m.structs[name]; ok {
		return name
	}
	return ""
}

// refDepth bounds the walk below the listed element:
// properties.networkProfile.networkInterfaces is a ref, deeper is detail.
const refDepth = 3

// refsOf lists the JSON paths on a listed element that reference other ARM
// resources, by shape: a sub-resource struct (an ID and at most one other
// field), a string the generator spells as an identifier (ID/IDs suffix: it
// rewrites every JSON "…Id" that way), or a URI/URL string beside a
// sub-resource sibling (a key URL next to its source vault). The element's
// own ID is not a ref. Names are the JSON names from the model's serde code,
// so the paths match what a scanner stores. No field name is special:
// managedBy and a key vault URI with no vault sibling are not refs.
func (m *module) refsOf(element string) []string {
	if element == "" {
		return nil
	}
	refs := map[string]bool{}
	m.walkRefs(element, "", 0, refs)
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for r := range refs {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (m *module) walkRefs(typ, prefix string, depth int, refs map[string]bool) {
	vault := slices.ContainsFunc(m.structs[typ], func(f field) bool {
		base := m.model(strings.TrimLeft(f.typ, "[]*"))
		return base != "" && m.isRefStruct(base)
	})
	for _, f := range m.structs[typ] {
		if depth == 0 && f.name == "ID" {
			continue
		}
		wire, ok := m.wire[typ][f.name]
		if !ok {
			continue // not serialized: no JSON path names it
		}
		path := prefix + wire
		base := m.model(strings.TrimLeft(f.typ, "[]*"))
		switch {
		case base != "" && m.isRefStruct(base):
			refs[path] = true
		case f.typ == "*string" || f.typ == "[]*string":
			if strings.HasSuffix(f.name, "ID") || strings.HasSuffix(f.name, "IDs") ||
				vault && (strings.HasSuffix(f.name, "URI") || strings.HasSuffix(f.name, "URL")) {
				refs[path] = true
			}
		case depth < refDepth && base != "":
			m.walkRefs(base, path+".", depth+1, refs)
		}
	}
}

// isRefStruct: a struct that is nothing but a pointer to another resource.
func (m *module) isRefStruct(typ string) bool {
	fields := m.structs[typ]
	hasID := false
	for _, f := range fields {
		if f.name == "ID" && f.typ == "*string" {
			hasID = true
		}
	}
	return hasID && len(fields) <= 2
}

// armEnvelope reports whether a model carries the ARM proxy-resource envelope:
// SystemData, or both ID and Type. It is what separates a listing of resources
// the subscription owns from a published catalog of the provider's own data.
func (m *module) armEnvelope(element string) bool {
	var hasID, hasType bool
	for _, f := range m.structs[element] {
		switch f.name {
		case "SystemData":
			return true
		case "ID":
			hasID = true
		case "Type":
			hasType = true
		}
	}
	return hasID && hasType
}
