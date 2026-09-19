package main

import (
	"fmt"
	"go/format"
	"strings"
	"unicode"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

// scannerSig maps a provider to the (imports, serviceEntry-fn signature) its
// stub scanner needs. The stub returns (0,0,nil), so parameters are unused but
// their types must resolve — hence the per-provider import set.
type scannerSig struct {
	imports []string
	sig     string // the scan<Svc> parameter list + return, without the "func scanX" prefix
	body    string // TODO guidance for the stub body
}

var scannerSigs = map[string]scannerSig{
	"aws": {
		imports: []string{"context", "github.com/icearp/disco-cli/internal/restype", "github.com/icearp/disco-cli/store"},
		sig:     "(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error)",
		body:    "build the SDK client (svc.NewFromConfig(acct.cfg, ...)), paginate the\n\t// List/Describe ops, map each item to *store.Resource, then st.UpsertResources(batch).\n\t// Split out scan%[1]sWithClient(ctx, client, ...) for a fake-transport test seam.",
	},
	"gcp": {
		imports: []string{"context", "github.com/icearp/disco-cli/internal/restype", "github.com/icearp/disco-cli/store"},
		sig:     "(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error)",
		body:    "build the google.golang.org/api service client, paginate the list ops via\n\t// runPaginated, map each item to *store.Resource, then upsertWithProjClosure(p, st, batch).\n\t// Add a scan%[1]sWithClient seam for a fake-server test.",
	},
	"azure": {
		imports: []string{"context", "github.com/Azure/azure-sdk-for-go/sdk/azcore", "github.com/icearp/disco-cli/internal/restype", "github.com/icearp/disco-cli/store"},
		sig:     "(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error)",
		body:    "build the arm* client with cred, page via azPageScan, map each item to\n\t// *store.Resource, then st.UpsertResources(batch). Add a scan%[1]sWithClient seam.",
	},
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
	sig, ok := scannerSigs[provName]
	if !ok {
		// Unknown provider signature: emit descriptors only, no scanner skeleton.
		sig = scannerSig{imports: []string{"github.com/icearp/disco-cli/internal/restype"}}
	}
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
			out = append(out, kebab(displaySingular(sg)))
		}
	}
	return out
}

// displaySingular is the spelling shown in a committed const and type string.
// sdkinv.Singular is an equality stem and says so: it answers "timeseries"
// with "timesery" and "thesauri" with "thesauri", which is right for a
// comparison and wrong in generated source, so the shapes it cannot spell are
// left as the SDK spells them.
func displaySingular(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.HasSuffix(l, "series"), strings.HasSuffix(l, "species"), strings.HasSuffix(l, "data"), strings.HasSuffix(l, "metadata"):
		return s
	case strings.HasSuffix(l, "i"), strings.HasSuffix(l, "ices"):
		return s // "thesauri", "indices": Latin plurals Singular cannot spell
	}
	return sdkinv.Singular(s)
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
