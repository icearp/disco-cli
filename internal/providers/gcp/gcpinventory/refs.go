package gcpinventory

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// schema is a Discovery JSON schema, named or inline: bigquery lists datasets
// as an array of inline objects, and storage nests encryption.defaultKmsKeyName
// in an inline object. Named schemas decode lazily per document: most are
// request bodies and enums the walk never reaches.
type schema struct {
	Type                 string             `json:"type"`
	Ref                  string             `json:"$ref"`
	Description          string             `json:"description"`
	Properties           map[string]*schema `json:"properties"`
	Items                *schema            `json:"items"`
	AdditionalProperties *schema            `json:"additionalProperties"`
}

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

// resolve follows a $ref; an inline schema is itself.
func (ss *schemaSet) resolve(s *schema) *schema {
	if s != nil && s.Ref != "" {
		return ss.get(s.Ref)
	}
	return s
}

// elem is the schema of one listed item: named (ref) or inline (s only).
// viaMap marks an aggregated list, whose items sit in a per-scope map value.
type elem struct {
	ref    string
	s      *schema
	viaMap bool
}

// element resolves a list response to the schema of one listed item: an
// array property's item schema, or for an aggregated list the array inside
// the per-scope map value.
//
// A response may carry several arrays (ListJobsResponse has failedLocation
// beside jobs), so the one named after the collection wins, then the richest
// item schema, then the alphabetically first. Taking the first outright gave
// dataflow/jobs FailedLocation's zero refs over Job's 17.
func (ss *schemaSet) element(response, noun string, seen map[string]bool) elem {
	s := ss.get(response)
	// Self-referential schemas exist in the cache (discovery JsonSchema,
	// BackendRule, dataflow BoundedTrieNode); an unguarded recursion would
	// abort the command.
	if s == nil || seen[response] {
		return elem{}
	}
	seen[response] = true
	defer delete(seen, response)
	names := slices.Sorted(maps.Keys(s.Properties))
	var best elem
	bestProps := -1
	want := sdkinv.Ident(noun)
	for _, n := range names {
		p := s.Properties[n]
		if p.Type != "array" || p.Items == nil {
			continue
		}
		item := ss.resolve(p.Items)
		if item == nil || (p.Items.Ref == "" && len(item.Properties) == 0) {
			continue // an array of strings lists values, not items
		}
		e := elem{ref: p.Items.Ref, s: item}
		if want != "" && (sdkinv.Ident(n) == want || (e.ref != "" && sdkinv.Ident(e.ref) == want)) {
			return e
		}
		if len(item.Properties) > bestProps {
			best, bestProps = e, len(item.Properties)
		}
	}
	if best.s != nil {
		return best
	}
	for _, n := range names {
		p := s.Properties[n]
		if p.Type == "object" && p.AdditionalProperties != nil && p.AdditionalProperties.Ref != "" {
			if e := ss.element(p.AdditionalProperties.Ref, noun, seen); e.s != nil {
				e.viaMap = true
				return e
			}
		}
	}
	return elem{}
}

// untypedArray reports an array property whose items the schema leaves
// untyped ("any").
func (ss *schemaSet) untypedArray(response string) bool {
	s := ss.get(response)
	return s != nil && slices.ContainsFunc(slices.Collect(maps.Values(s.Properties)), func(p *schema) bool {
		return p != nil && p.Type == "array" && p.Items != nil && p.Items.Ref == "" && (p.Items.Type == "" || p.Items.Type == "any")
	})
}

// refsOf lists the properties on a listed element that name other resources,
// at any depth: nested objects, array items and map values ("labels.{}.x"),
// named or inline. Only a $ref cycle stops the walk.
func (ss *schemaSet) refsOf(e elem) []string {
	if e.s == nil {
		return nil
	}
	refs := map[string]bool{}
	seen := map[string]bool{}
	if e.ref != "" {
		seen[e.ref] = true
	}
	ss.walk(e.s, "", true, refs, seen)
	if len(refs) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(refs))
}

func (ss *schemaSet) walk(s *schema, prefix string, top bool, refs, seen map[string]bool) {
	for _, pn := range slices.Sorted(maps.Keys(s.Properties)) {
		p := s.Properties[pn]
		// JSON-schema keys ($ref, $schema, $id) describe the document, not a
		// resource; their descriptions read exactly like a reference.
		if p == nil || (top && ownProps[pn]) || strings.HasPrefix(pn, "$") {
			continue
		}
		path := prefix + pn
		switch {
		case p.Type == "string":
			if isRef(pn, p.Description) {
				refs[path] = true
			}
		case p.Type == "array" && p.Items != nil:
			if p.Items.Type == "string" && isRef(pn, p.Description) {
				refs[path] = true
			}
			ss.descend(p.Items, path+".", refs, seen)
		case p.AdditionalProperties != nil:
			ss.descend(p.AdditionalProperties, path+".{}.", refs, seen)
		default:
			ss.descend(p, path+".", refs, seen)
		}
	}
}

// descend walks a nested schema, following a $ref at most once per path.
func (ss *schemaSet) descend(s *schema, prefix string, refs, seen map[string]bool) {
	if s.Ref != "" {
		if seen[s.Ref] {
			return
		}
		seen[s.Ref] = true
		defer delete(seen, s.Ref)
	}
	if t := ss.resolve(s); t != nil {
		ss.walk(t, prefix, false, refs, seen)
	}
}

func isRef(name, desc string) bool {
	return refNameRe.MatchString(name) || refDescRe.MatchString(desc) || strings.HasSuffix(name, "Name") && strings.Contains(strings.ToLower(desc), "resource")
}
