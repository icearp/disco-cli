package aws

import (
	"sort"
	"strings"
)

// refDepth bounds the walk below the listed element: VpcConfig.SubnetIds
// is a ref, deeper nesting is configuration detail.
const refDepth = 2

// refsOf lists the id-like members on the elements an operation returns,
// minus the element's own id. A lister's elements are its collection
// members; a detail read's element is its single structure member.
func refsOf(m *smithyModel, sh *shape, nounCanon string) []string {
	if sh.Output == nil {
		return nil
	}
	out := m.Shapes[sh.Output.Target]
	if out == nil {
		return nil
	}
	refs := map[string]bool{}
	var elements []*shape
	for _, mem := range out.Members {
		if t := m.Shapes[mem.Target]; t != nil && t.Type == "list" && t.Member != nil {
			if el := m.Shapes[t.Member.Target]; el != nil && el.Type == "structure" {
				elements = append(elements, el)
			}
		}
	}
	if len(elements) == 0 && len(out.Members) == 1 {
		for _, mem := range out.Members {
			if el := m.Shapes[mem.Target]; el != nil && el.Type == "structure" {
				elements = append(elements, el)
			}
		}
	}
	for _, el := range elements {
		walkRefs(m, el, "", 0, nounCanon, refs)
	}
	if len(refs) == 0 {
		return nil
	}
	list := make([]string, 0, len(refs))
	for r := range refs {
		list = append(list, r)
	}
	sort.Strings(list)
	return list
}

func walkRefs(m *smithyModel, el *shape, prefix string, depth int, nounCanon string, refs map[string]bool) {
	for name, mem := range el.Members {
		t := m.Shapes[mem.Target]
		if t == nil { // prelude primitive (smithy.api#String): never in the model file
			if idLikeRe.MatchString(name) && !ownID(name, depth, nounCanon) {
				refs[prefix+name] = true
			}
			continue
		}
		switch {
		case t.Type == "structure":
			if depth < refDepth {
				walkRefs(m, t, prefix+name+".", depth+1, nounCanon, refs)
			}
		case t.Type == "list" && t.Member != nil:
			el := m.Shapes[t.Member.Target]
			if el != nil && el.Type == "structure" {
				if depth < refDepth {
					walkRefs(m, el, prefix+name+".", depth+1, nounCanon, refs)
				}
			} else if idLikeRe.MatchString(name) && !ownID(name, depth, nounCanon) {
				refs[prefix+name] = true
			}
		case idLikeRe.MatchString(name) && !ownID(name, depth, nounCanon):
			refs[prefix+name] = true
		}
	}
}

// ownID is the element's own identity: a bare Arn/Id/Name, or one stemmed
// with the element's noun (InstanceId on an instance), at the top level.
func ownID(name string, depth int, nounCanon string) bool {
	if depth > 0 {
		return false
	}
	stem := memberStem(name)
	return stem == "" || stem == nounCanon || strings.HasSuffix(nounCanon, stem)
}
