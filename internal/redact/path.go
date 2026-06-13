package redact

import "strings"

// segKind enumerates the three operators supported in rule paths.
type segKind int

const (
	segLiteral segKind = iota
	segMapWildcard
	segArrayWildcard
)

type segment struct {
	kind segKind
	name string // populated only for segLiteral
}

// compilePath parses a dotted path into segments.
//
// Operators:
//   - "Foo"        — literal map key
//   - "*"          — every map key at this level
//   - "Foo[*]"     — literal map key whose value is an array; descend each element
//   - "[*]"        — bare array wildcard (rare; only after a literal that produced a slice)
//
// Multiple `[*]` may chain (`Foo[*][*]`) for nested arrays.
func compilePath(path string) []segment {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	out := make([]segment, 0, len(parts))
	for _, p := range parts {
		// Strip trailing [*] tokens, defer them as array-wildcard segments.
		trailing := 0
		for strings.HasSuffix(p, "[*]") {
			p = p[:len(p)-3]
			trailing++
		}
		switch p {
		case "*":
			out = append(out, segment{kind: segMapWildcard})
		case "":
			// All-empty after stripping trailing [*]: pure array-wildcard chain.
		default:
			out = append(out, segment{kind: segLiteral, name: p})
		}
		for i := 0; i < trailing; i++ {
			out = append(out, segment{kind: segArrayWildcard})
		}
	}
	return out
}
