package gcp

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
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
	// ownProps are the element's own identity and bookkeeping.
	ownProps = map[string]bool{"selfLink": true, "id": true, "name": true, "kind": true, "etag": true, "uid": true, "description": true, "creationTimestamp": true, "createTime": true, "updateTime": true, "selfLinkWithId": true}
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
func (ss *schemaSet) element(response string) string {
	s := ss.get(response)
	if s == nil {
		return ""
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := s.Properties[n]
		if p.Type == "array" && p.Items != nil && p.Items.Ref != "" {
			return p.Items.Ref
		}
	}
	for _, n := range names {
		p := s.Properties[n]
		if p.Type == "object" && p.AdditionalProperties != nil && p.AdditionalProperties.Ref != "" {
			if el := ss.element(p.AdditionalProperties.Ref); el != "" {
				return el
			}
		}
	}
	return ""
}

// refsOf lists the properties on a listed element that name other
// resources, at any depth up to refDepth.
func (ss *schemaSet) refsOf(response string) []string {
	el := ss.element(response)
	if el == "" {
		return nil
	}
	refs := map[string]bool{}
	ss.walk(el, "", 0, refs, map[string]bool{})
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

func isRef(name, desc string) bool {
	return refNameRe.MatchString(name) || refDescRe.MatchString(desc) || strings.HasSuffix(name, "Name") && strings.Contains(strings.ToLower(desc), "resource")
}
