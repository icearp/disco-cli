package gcpinventory

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Discovery schema subset, decoded lazily per document: most schemas are
// request bodies and enums the ref walk never reaches.
type schema struct {
	Properties           map[string]*prop `json:"properties"`
	AdditionalProperties *prop            `json:"additionalProperties"`
}

type prop struct {
	Type                 string `json:"type"`
	Ref                  string `json:"$ref"`
	Description          string `json:"description"`
	Items                *prop  `json:"items"`
	AdditionalProperties *prop  `json:"additionalProperties"`
}

// refDepth bounds the walk below the listed element:
// networkInterfaces.subnetwork is a ref, deeper is detail.
const refDepth = 2

var (
	// Discovery strings carry no type for resource references; the field name
	// or the description says it is a URL, a resource name, a key or an account.
	refNameRe = regexp.MustCompile(`(Link|Links|Url|Urls|Uri|Uris|Id|Ids|Ref|Refs|Account|Network|Subnetwork)$`)
	refDescRe = regexp.MustCompile(`(?i)\b(URL|URI|resource name|fully[- ]qualified|reference to|service account|email address|KMS|in the format|of the form)\b`)
	// ownProps are the element's own identity and bookkeeping. displayName,
	// generateName, clientOperationId and revisionId join them because they
	// read as refs to refDescRe while naming nothing outside the element.
	ownProps = map[string]bool{"selfLink": true, "id": true, "name": true, "kind": true, "etag": true, "uid": true, "description": true, "creationTimestamp": true, "createTime": true, "updateTime": true, "selfLinkWithId": true, "displayName": true, "generateName": true, "clientOperationId": true, "revisionId": true}
)

type schemaSet struct {
	raw    map[string]json.RawMessage
	parsed map[string]*schema
}

func (ss *schemaSet) get(name string) *schema {
	if s, ok := ss.parsed[name]; ok {
		return s
	}
	var s *schema
	if raw, ok := ss.raw[name]; ok {
		s = &schema{}
		if json.Unmarshal(raw, s) != nil {
			s = nil
		}
	}
	if ss.parsed == nil {
		ss.parsed = map[string]*schema{}
	}
	ss.parsed[name] = s
	return s
}

// element resolves a list response to the schema of one listed item: the
// array property's item schema, or for an aggregated list the array inside
// the per-scope map value.
//
// A response may carry several arrays of schemas (ListJobsResponse has
// failedLocation beside jobs), so the one named after the collection wins,
// then the richest item schema, then the alphabetically first. Taking the
// first outright gave dataflow/jobs FailedLocation's zero refs over Job's 17.
func (ss *schemaSet) element(response, noun string, seen map[string]bool) string {
	s := ss.get(response)
	// Self-referential schemas exist in the cache (discovery JsonSchema,
	// BackendRule, dataflow BoundedTrieNode); none is reachable from a list
	// response today, and an unguarded recursion would abort the command.
	if s == nil || seen[response] {
		return ""
	}
	seen[response] = true
	defer delete(seen, response)
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	var best string
	var bestProps int
	want := sdkinv.Ident(noun)
	for _, n := range names {
		p := s.Properties[n]
		if p.Type != "array" || p.Items == nil || p.Items.Ref == "" {
			continue
		}
		if want != "" && (sdkinv.Ident(n) == want || sdkinv.Ident(p.Items.Ref) == want) {
			return p.Items.Ref
		}
		if item := ss.get(p.Items.Ref); best == "" || len(item.props()) > bestProps {
			best, bestProps = p.Items.Ref, len(item.props())
		}
	}
	if best != "" {
		return best
	}
	for _, n := range names {
		p := s.Properties[n]
		if p.Type == "object" && p.AdditionalProperties != nil && p.AdditionalProperties.Ref != "" {
			if el := ss.element(p.AdditionalProperties.Ref, noun, seen); el != "" {
				return el
			}
		}
	}
	return ""
}

// refsOf lists the properties on a listed element that name other
// resources, at any depth up to refDepth.
func (ss *schemaSet) refsOf(response, noun string) []string {
	seen := map[string]bool{}
	el := ss.element(response, noun, seen)
	if el == "" {
		return nil
	}
	refs := map[string]bool{}
	ss.walk(el, "", 0, refs, seen)
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

func (ss *schemaSet) walk(name, prefix string, depth int, refs, seen map[string]bool) {
	s := ss.get(name)
	if s == nil || seen[name] {
		return
	}
	seen[name] = true
	for pn, p := range s.Properties {
		if depth == 0 && ownProps[pn] {
			continue
		}
		// JSON-schema keys ($ref, $schema, $id) describe the document, not a
		// resource; their descriptions read exactly like a reference.
		if strings.HasPrefix(pn, "$") {
			continue
		}
		path := prefix + pn
		nested := p.Ref
		if p.Type == "array" && p.Items != nil {
			nested = p.Items.Ref
			if p.Items.Type == "string" && isRef(pn, p.Description) {
				refs[path] = true
			}
		}
		switch {
		case nested != "" && depth < refDepth:
			ss.walk(nested, path+".", depth+1, refs, seen)
		case p.Type == "string" && isRef(pn, p.Description):
			refs[path] = true
		}
	}
	delete(seen, name)
}

// props is nil-safe: an unresolvable $ref has no properties to count.
func (s *schema) props() map[string]*prop {
	if s == nil {
		return nil
	}
	return s.Properties
}

func isRef(name, desc string) bool {
	return refNameRe.MatchString(name) || refDescRe.MatchString(desc) || strings.HasSuffix(name, "Name") && strings.Contains(strings.ToLower(desc), "resource")
}
