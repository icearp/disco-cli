package sdkinv

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Canon reduces a name to lowercase alphanumerics so spellings that differ
// only by separators or case compare equal ("virtualMachines" ==
// "virtual-machines" == "VirtualMachines"). It is the only cross-source key
// comparison the extractors use.
func Canon(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Singular strips a plural suffix for *equality* purposes only. Both sides of
// every comparison pass through it, so an imperfect stem ("indexes" →
// "indexe") still compares equal to itself; never use the result for display.
func Singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "yses") && len(s) > 4: // analyses
		return s[:len(s)-2] + "is"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "ses") && len(s) > 3:
		// "-ses" hides two shapes: an "-s" singular (status, alias, lens)
		// and an "-se" singular (database, case, release); the letter before
		// the stem's "s" tells them apart well enough for SDK nouns.
		if stem := s[:len(s)-3]; strings.HasSuffix(stem, "u") || strings.HasSuffix(stem, "ia") || strings.HasSuffix(stem, "n") {
			return s[:len(s)-2]
		}
		return s[:len(s)-1]
	case strings.HasSuffix(s, "ss"), strings.HasSuffix(s, "us"), strings.HasSuffix(s, "sis"), strings.HasSuffix(s, "ias"):
		return s
	case strings.HasSuffix(s, "s") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
}

// CanonSingular is Canon followed by Singular.
func CanonSingular(s string) string { return Singular(Canon(s)) }

// Ident is the cross-source equality stem: Canon, "-ies"→"y", then every
// trailing "e"/"s" dropped, so a plural and its singular meet even where
// Singular cannot know the stem ("caches"/"cache", "aliases"/"alias",
// "statuses"/"status", "accesses"/"access"). Never display it.
func Ident(s string) string {
	c := Canon(s)
	switch {
	case strings.HasSuffix(c, "ies") && len(c) > 3:
		return c[:len(c)-3] + "y"
	case strings.HasSuffix(c, "yses") && len(c) > 4: // analyses / analysis
		c = c[:len(c)-2] + "is"
	}
	return strings.TrimRight(c, "es")
}

// Kebab converts camelCase / PascalCase to kebab-case, keeping digit runs
// attached ("virtualMachineScaleSets" → "virtual-machine-scale-sets",
// "p2sVpnGateways" → "p2s-vpn-gateways").
func Kebab(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	prevLower := false
	for i, r := range s {
		if unicode.IsUpper(r) {
			// Split before an upper that ends a lowercase run, or that starts a
			// new word after an acronym ("HSMs": the trailing plural 's' does not
			// start a word).
			nextLower := i+1 < len(s) && unicode.IsLower(rune(s[i+1]))
			pluralTail := nextLower && s[i+1] == 's' && (i+2 == len(s) || !unicode.IsLower(rune(s[i+2])))
			if i > 0 && (prevLower || (nextLower && !pluralTail)) {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
			continue
		}
		if r == '_' || r == ' ' || r == '.' {
			b.WriteByte('-')
			prevLower = false
			continue
		}
		b.WriteRune(r)
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	return b.String()
}

// LowerFirst / UpperFirst flip the first byte's case (ASCII identifiers).
func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// UpperFirst is the inverse of LowerFirst.
func UpperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// SortCandidates orders by Key so output is deterministic across runs.
func SortCandidates(cs []Candidate) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Key < cs[j].Key })
	for i := range cs {
		SortOps(cs[i].Ops) // shared signing names (rds, neptune, docdb) repeat a label across modules
	}
}

// SortOps orders operations by label then module.
func SortOps(ops []Operation) {
	sort.Slice(ops, func(a, b int) bool {
		x, y := ops[a], ops[b]
		// Label+Module alone is not a total order: one Smithy model can carry
		// two operation shapes with the same name in different namespaces
		// (healthlake), differing only in Required, and an unstable sort then
		// flipped their order between runs of the same binary.
		switch {
		case x.Label != y.Label:
			return x.Label < y.Label
		case x.Module != y.Module:
			return x.Module < y.Module
		case x.Name != y.Name:
			return x.Name < y.Name
		case x.Path != y.Path:
			return x.Path < y.Path
		case x.IsList != y.IsList:
			return y.IsList
		case x.Paged != y.Paged:
			return y.Paged
		}
		return strings.Join(x.Required, ",")+"\x00"+strings.Join(x.Targets, ",") <
			strings.Join(y.Required, ",")+"\x00"+strings.Join(y.Targets, ",")
	})
}

// keyBadRe are the shapes a candidate key must never contain: a template
// placeholder, a query fragment or an empty segment mean the extractor keyed
// something it did not parse, and the key then matches no type and no registry
// entry.
var keyBadRe = regexp.MustCompile(`[{}?=$]|//`)

// serviceRe is the shape of a service segment: lower-case, digits, dot, dash,
// underscore (Discovery names one API prod_tt_sasportal).
var serviceRe = regexp.MustCompile(`^[a-z0-9._-]+$`)

// ValidateKey reports why a candidate key is malformed, or "" when it is well
// formed. Extractors run it over the live universe, not only the fixtures:
// every shape below shipped in docs/coverage.md at some point.
func ValidateKey(key, service string) string {
	switch {
	case service == "":
		return "empty service"
	case !serviceRe.MatchString(service):
		return "service outside [a-z0-9.-]+"
	case !strings.HasPrefix(key, service+"/"):
		return "key does not start with the service"
	case keyBadRe.MatchString(key):
		return "key carries a placeholder, query fragment or empty segment"
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" {
			return "key has an empty segment"
		}
	}
	return ""
}

// StrongerClass returns the class that wins when two ops describe one key:
// resource > catalog > non-resource > attribute. A detail read (attribute)
// never outranks a lister on the same key: the Get is that collection's own
// read, so the lister decides what the collection is.
func StrongerClass(a, b Class) Class {
	if a == "" {
		return b
	}
	rank := map[Class]int{ClassResource: 3, ClassCatalog: 2, ClassNonResource: 1, ClassAttribute: 0}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
