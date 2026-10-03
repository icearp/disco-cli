package main

import (
	"fmt"
	"go/format"
	"strings"
	"unicode"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

// scannerSig is the stub scanner a provider's coverage.ScaffoldStubber
// describes.
type scannerSig struct {
	imports []string
	sig     string // the scan<Svc> parameter list + return, without the "func scanX" prefix
	body    string // TODO guidance for the stub body
}

// stubFor returns provName's stub, or descriptors-only when the provider does
// not implement coverage.ScaffoldStubber.
func stubFor(provName string) scannerSig {
	if p, ok := coverage.Get(provName); ok {
		if s, ok := p.(coverage.ScaffoldStubber); ok {
			imports, sig, body := s.ScannerStub()
			return scannerSig{imports: imports, sig: sig, body: body}
		}
	}
	return scannerSig{imports: []string{"github.com/icearp/disco-cli/internal/restype"}}
}

// scaffoldOpts carry what the generator can only learn from the live package:
// whether the service is already registered (emitting a second
// registerService panics the provider at init, and a second func scan<Svc> is
// a compile error) and which disco type strings already exist.
type scaffoldOpts struct {
	serviceRegistered bool
	scanFnExists      bool
	existingTypes     map[string]bool
}

// genScaffold renders a self-contained <svc>_scanners.go: the Type* consts,
// the registerType descriptors (each annotated with the SDK list ops, depth
// and scope the candidate was derived from), a registerService call, and a
// stub scanner returning (0,0,nil). It compiles as-is; the human fills the
// body and later lifts the consts into <provider>_types.go.
//
// An error means the emitted source would not compile or would redeclare an
// existing type; the caller prints it and writes nothing.
func genScaffold(provName, service string, rows []coverage.Row, opts scaffoldOpts) (string, error) {
	sig := stubFor(provName)
	svcFn := pascal(service)
	emitScanner := sig.sig != "" && !opts.serviceRegistered && !opts.scanFnExists

	var consts, descs strings.Builder
	var conflicts []string
	seen := map[string]bool{}
	for _, r := range rows {
		segs := resourceSegments(r.Key, service)
		discoType := provName + ":" + service + ":" + strings.Join(segs, ":")
		constName := "Type" + svcFn
		for _, seg := range segs {
			constName += pascal(seg)
		}
		switch {
		case seen[discoType]:
			conflicts = append(conflicts, fmt.Sprintf("%s: %s would be declared twice", r.Key, discoType))
			continue
		case opts.existingTypes[discoType]:
			conflicts = append(conflicts, fmt.Sprintf("%s: %s is already declared by this provider", r.Key, discoType))
			continue
		}
		seen[discoType] = true
		fmt.Fprintf(&consts, "\t%s = %q\n", constName, discoType)
		fmt.Fprintf(&descs, "\t// %s: ops %s; depth %d", r.Key, strings.Join(r.Ops, ", "), r.Depth)
		if r.Parent != "" {
			fmt.Fprintf(&descs, " (parent %s)", r.Parent)
		}
		if r.Scope != "" {
			fmt.Fprintf(&descs, "; scope %s", r.Scope)
		}
		if len(r.Refs) > 0 {
			fmt.Fprintf(&descs, "; refs %s", strings.Join(r.Refs, ", "))
		}
		fmt.Fprintf(&descs, "\n\tregisterType(restype.Descriptor{Type: %s, Service: %q})\n", constName, service)
	}

	if len(conflicts) > 0 {
		return "", fmt.Errorf("scaffold would not compile:\n  %s", strings.Join(conflicts, "\n  "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", provName)
	b.WriteString("// Code scaffolded by cmd/disco-scaffold — VERIFY before use.\n")
	b.WriteString("// Const names + disco type strings are best-effort derived from the SDK\n")
	b.WriteString("// candidate key; reconcile them with <provider>_types.go naming conventions,\n")
	b.WriteString("// then move the consts there. The scanner body is a TODO stub returning (0,0,nil).\n")
	if sig.sig != "" && !emitScanner {
		// A second registerService for a registered name panics every provider
		// at init, and a second func scan<Svc> does not compile.
		fmt.Fprintf(&b, "//\n// %s is already scanned by this package (registerService and/or\n// func scan%s exist), so neither is emitted: wire these types into the\n// existing scanner instead.\n", provName+":"+service, svcFn)
	}
	b.WriteString("\n")
	if len(sig.imports) > 0 {
		b.WriteString("import (\n")
		for _, imp := range sig.imports {
			fmt.Fprintf(&b, "\t%q\n", imp)
		}
		b.WriteString(")\n\n")
	}
	fmt.Fprintf(&b, "const (\n%s)\n\n", consts.String())
	b.WriteString("func init() {\n")
	b.WriteString(descs.String())
	if emitScanner {
		fmt.Fprintf(&b, "\tregisterService(serviceEntry{name: %q, fn: scan%s})\n", provName+":"+service, svcFn)
	}
	b.WriteString("}\n")
	if emitScanner {
		fmt.Fprintf(&b, "\nfunc scan%s%s {\n", svcFn, sig.sig)
		fmt.Fprintf(&b, "\t// TODO: "+sig.body+"\n", svcFn)
		b.WriteString("\treturn 0, 0, nil\n}\n")
	}
	// gofmt the result so the emitted file is drop-in clean. On the (unexpected)
	// event of a syntax error, return the raw source so the human can debug it.
	if formatted, err := format.Source([]byte(b.String())); err == nil {
		return string(formatted), nil
	}
	return b.String(), nil
}

// resourceSegments are a candidate key's path below the service, each
// singularised for display. The parent segments belong in both the type string
// and the const name: azure children are colon-paths by convention, and
// dropping the parent made virtualmachines/runcommands and
// virtualmachinescalesets/virtualmachines/runcommands one declaration twice.
func resourceSegments(key, service string) []string {
	path := key
	if rest, ok := strings.CutPrefix(key, service+"/"); ok {
		path = rest
	} else if i := strings.Index(key, "/"); i >= 0 {
		path = key[i+1:]
	}
	segs := strings.Split(path, "/")
	out := make([]string, 0, len(segs))
	for _, sg := range segs {
		if sg != "" {
			out = append(out, kebab(sdkinv.Singular(sg)))
		}
	}
	return out
}

// splitWords breaks an identifier into lowercase word tokens across camelCase,
// acronym runs (RestAPI -> rest, api), digit boundaries, and -/_/. separators.
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '-' || r == '_' || r == '.' || r == ' ':
			flush()
			continue
		case i > 0 && unicode.IsUpper(r) && unicode.IsLower(rs[i-1]):
			// camel boundary: fooBar -> foo|Bar
			flush()
		case i > 0 && unicode.IsUpper(r) && unicode.IsDigit(rs[i-1]):
			// digit -> Upper starts a new word: S3Bucket -> s3|bucket
			flush()
		case i > 0 && unicode.IsUpper(r) && i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1]):
			// acronym end: RESTApi -> rest|Api
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// kebab renders an identifier as lowercase-hyphenated (disco resource-segment
// shape): "RestApi" -> "rest-api", "virtualMachines" -> "virtual-machines".
func kebab(s string) string { return strings.Join(splitWords(s), "-") }

// pascal renders an identifier as PascalCase for a Go const name:
// "rest-api" -> "RestApi", "virtualMachines" -> "VirtualMachines".
func pascal(s string) string {
	words := splitWords(s)
	for i, w := range words {
		if w == "" {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, "")
}
