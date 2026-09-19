package sdkinv

import "strings"

// Segment is one element of a REST path template.
type Segment struct {
	Text  string
	Param bool // "{name}" placeholder
}

// ParseTemplate splits "/a/{b}/c" into segments; "{+x}" and "{x}" are params.
// A query string is cut first: a segment is a param only when it both opens
// and closes with braces, so "{apiId}?export=true" parsed as a static and the
// item GET it belongs to was admitted as a collection.
func ParseTemplate(tmpl string) []Segment {
	if i := strings.IndexByte(tmpl, '?'); i >= 0 {
		tmpl = tmpl[:i]
	}
	parts := strings.Split(strings.Trim(tmpl, "/"), "/")
	out := make([]Segment, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			out = append(out, Segment{Text: strings.Trim(p, "{}+"), Param: true})
			continue
		}
		out = append(out, Segment{Text: p})
	}
	return out
}

// ResourcePath is the scope-free view of a collection path template.
type ResourcePath struct {
	Statics []string // static segments that name collections, outermost first
	Parents []string // Statics[i] that are followed by a {param}; the last static is the collection itself
	Item    bool     // template ends in a {param}: an item path, not a collection
}

// StripScopes removes "<scope>/{param}" pairs (and bare scope literals) so
// what remains names the resource hierarchy. scopes and literals are keyed by
// lowercase static name (ARM paths spell resourceGroups both ways); a scope's
// following {param} is a container, not a parent resource.
func StripScopes(segs []Segment, scopes map[string]bool, literals map[string]bool) ResourcePath {
	var rp ResourcePath
	for i := 0; i < len(segs); i++ {
		s := segs[i]
		if s.Param {
			continue
		}
		lower := strings.ToLower(s.Text)
		if literals[lower] {
			continue
		}
		// A scope pair strips only when more path follows it; a trailing
		// "<scope>/{param}" is the container resource itself (resource group
		// GET, management group GET).
		if scopes[lower] && i+2 < len(segs) && segs[i+1].Param {
			i++
			continue
		}
		rp.Statics = append(rp.Statics, s.Text)
		if i+1 < len(segs) && segs[i+1].Param {
			rp.Parents = append(rp.Parents, s.Text)
		}
	}
	if n := len(segs); n > 0 && segs[n-1].Param {
		rp.Item = true
		// The trailing param belongs to the last static; it is the item, not a parent.
		if len(rp.Parents) > 0 && len(rp.Statics) > 0 && rp.Parents[len(rp.Parents)-1] == rp.Statics[len(rp.Statics)-1] {
			rp.Parents = rp.Parents[:len(rp.Parents)-1]
			if len(rp.Parents) == 0 {
				rp.Parents = nil
			}
		}
	}
	return rp
}
