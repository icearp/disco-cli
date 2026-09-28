package awsinventory

import (
	"regexp"
	"sort"
	"strings"
)

// refDepth bounds the walk below the listed element: VpcConfig.SubnetIds
// is a ref, deeper nesting is configuration detail.
const refDepth = 2

// refsOf lists the id-like members on the elements an operation returns,
// minus the element's own id. A lister's elements are its collection members.
// A detail read has no collection: its elements are every structure member of
// the output, and when it has none — a flat read returning vpcId, subnetIds
// and loadBalancerArn directly — the output shape is itself the element.
// Recognising only the single-structure shape left 1,275 detail reads refless
// and presented 87 real resolver gaps as finished derived leaves.
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
	if len(elements) == 0 {
		for _, mem := range out.Members {
			if el := m.Shapes[mem.Target]; el != nil && el.Type == "structure" {
				elements = append(elements, el)
			}
		}
	}
	for _, el := range elements {
		walkRefs(m, el, "", 0, nounCanon, refs)
	}
	flatRefs(m, out, nounCanon, refs)
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

// flatRefs takes the output's own id-like primitives. A detail read often
// returns them beside the structures it wraps — m2's GetEnvironment answers
// with vpcId, subnetIds and loadBalancerArn next to a storageConfigurations
// list — and descending into the structures alone loses every one of them.
// Structure members are left to walkRefs so no path is emitted twice.
func flatRefs(m *smithyModel, out *shape, nounCanon string, refs map[string]bool) {
	for name, mem := range out.Members {
		target := mem.Target
		if t := m.Shapes[target]; t != nil && t.Type == "list" && t.Member != nil {
			target = t.Member.Target
		}
		if el := m.Shapes[target]; el != nil && el.Type == "structure" {
			continue
		}
		if isStringTarget(m, target) && isRefMember(name, 0, nounCanon) {
			refs[name] = true
		}
	}
}

func walkRefs(m *smithyModel, el *shape, prefix string, depth int, nounCanon string, refs map[string]bool) {
	for name, mem := range el.Members {
		t := m.Shapes[mem.Target]
		if t == nil { // prelude primitive (smithy.api#String): never in the model file
			if strings.HasSuffix(mem.Target, "#String") && isRefMember(name, depth, nounCanon) {
				refs[prefix+name] = true
			}
			continue
		}
		if t.Type == "map" && t.Value != nil { // a map's values are its elements
			t = &shape{Type: "list", Member: t.Value}
		}
		switch {
		case t.Type == "structure" || t.Type == "union":
			if depth < refDepth {
				walkRefs(m, t, prefix+name+".", depth+1, nounCanon, refs)
			}
		case t.Type == "list" && t.Member != nil:
			el := m.Shapes[t.Member.Target]
			if el != nil && (el.Type == "structure" || el.Type == "union") {
				if depth < refDepth {
					walkRefs(m, el, prefix+name+".", depth+1, nounCanon, refs)
				}
			} else if isStringTarget(m, t.Member.Target) && isRefMember(name, depth, nounCanon) {
				refs[prefix+name] = true
			}
		case t.Type == "string" && isRefMember(name, depth, nounCanon):
			refs[prefix+name] = true
		}
	}
}

// isStringTarget: a reference is a string that names another resource, so an
// enum, integer or long target is never one however id-like its member reads
// (ec2/instance's State.Name is an enum). Across all 431 models id-like
// members target string 54,129 times against 497 non-string, so this costs
// almost nothing and removes a whole class of false refs.
func isStringTarget(m *smithyModel, target string) bool {
	if t := m.Shapes[target]; t != nil {
		return t.Type == "string"
	}
	return strings.HasSuffix(target, "#String")
}

// tokenNameRe: idempotency tokens and optimistic-concurrency handles read as
// ids (CreatorRequestId, ClientToken, ETag) but name no resource.
var tokenNameRe = regexp.MustCompile(`(?:Token|ETag|Etag|RequestId|RequestID|RevisionId|RevisionID)s?$`)

// isRefMember: an id-like member that is neither the element's own identity
// nor a bookkeeping token.
func isRefMember(name string, depth int, nounCanon string) bool {
	return idLike(name) && !tokenNameRe.MatchString(name) && !ownID(name, depth, nounCanon)
}

// ownID is the element's own identity: a bare Arn/Id/Name, or one stemmed
// with the element's noun (InstanceId on an instance), at the top level.
//
// The exact-noun stem holds at every depth: a wrapper's own id restated
// inside it is still its own (DescribeInstances' element is Reservation, so
// InstanceId arrives at depth 1, and a union's arms restate the subject's ARN
// once per arm). The looser suffix rule stays depth-0 only — at depth it
// matches 682 genuine cross-resource references such as aoss/vpcendpoint's
// VpcId.
func ownID(name string, depth int, nounCanon string) bool {
	stem := memberStem(name)
	if stem == nounCanon {
		return true
	}
	if depth > 0 {
		return false
	}
	return stem == "" || strings.HasSuffix(nounCanon, stem)
}
