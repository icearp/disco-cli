package main

import (
	"fmt"
	"go/format"
	"strings"
	"unicode"

	"codeberg.org/icearp/disco/internal/coverage"
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
		imports: []string{"context", "codeberg.org/icearp/disco/internal/restype", "codeberg.org/icearp/disco/store"},
		sig:     "(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error)",
		body:    "build the SDK client (svc.NewFromConfig(acct.cfg, ...)), paginate the\n\t// List/Describe ops, map each item to *store.Resource, then st.UpsertResources(batch).\n\t// Split out scan%[1]sWithClient(ctx, client, ...) for a fake-transport test seam.",
	},
	"gcp": {
		imports: []string{"context", "codeberg.org/icearp/disco/internal/restype", "codeberg.org/icearp/disco/store"},
		sig:     "(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error)",
		body:    "build the google.golang.org/api service client, paginate the list ops via\n\t// runPaginated, map each item to *store.Resource, then upsertWithProjClosure(p, st, batch).\n\t// Add a scan%[1]sWithClient seam for a fake-server test.",
	},
	"azure": {
		imports: []string{"context", "github.com/Azure/azure-sdk-for-go/sdk/azcore", "codeberg.org/icearp/disco/internal/restype", "codeberg.org/icearp/disco/store"},
		sig:     "(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error)",
		body:    "build the arm* client with cred, page via azPageScan, map each item to\n\t// *store.Resource, then st.UpsertResources(batch). Add a scan%[1]sWithClient seam.",
	},
}

// genScaffold renders a self-contained <svc>_scanners.go: the Type* consts,
// the registerType descriptors (Upstream set only when the algorithmic key
// can't reproduce the upstream key), a registerService call, and a stub
// scanner returning (0,0,nil). It compiles as-is; the human fills the body and
// later lifts the consts into <provider>_types.go.
func genScaffold(provName, service string, rows []coverage.Row, prov coverage.Provider) string {
	sig, ok := scannerSigs[provName]
	if !ok {
		// Unknown provider signature: emit descriptors only, no scanner skeleton.
		sig = scannerSig{imports: []string{"codeberg.org/icearp/disco/internal/restype"}}
	}
	svcFn := pascal(service)

	var consts, descs strings.Builder
	for _, r := range rows {
		res := resourceSegment(provName, r.UpstreamKey)
		discoType := provName + ":" + service + ":" + kebab(res)
		constName := "Type" + svcFn + pascal(res)
		fmt.Fprintf(&consts, "\t%s = %q\n", constName, discoType)

		// Only carry Upstream when the algorithmic key can't reproduce the
		// upstream key — otherwise the alias is redundant. The human should
		// verify the derived disco type before trusting either path.
		up := ""
		if prov.AlgorithmicKey(discoType) != r.UpstreamKey {
			up = fmt.Sprintf(", Upstream: %q", r.UpstreamKey)
		}
		fmt.Fprintf(&descs, "\tregisterType(restype.Descriptor{Type: %s, Service: %q%s})\n", constName, service, up)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", provName)
	b.WriteString("// Code scaffolded by cmd/disco-scaffold — VERIFY before use.\n")
	b.WriteString("// Const names + disco type strings are best-effort derived from the upstream\n")
	b.WriteString("// key; reconcile them with <provider>_types.go naming conventions, then move\n")
	b.WriteString("// the consts there. The scanner body is a TODO stub returning (0,0,nil).\n\n")
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
	if sig.sig != "" {
		fmt.Fprintf(&b, "\tregisterService(serviceEntry{name: %q, fn: scan%s})\n", provName+":"+service, svcFn)
	}
	b.WriteString("}\n")
	if sig.sig != "" {
		fmt.Fprintf(&b, "\nfunc scan%s%s {\n", svcFn, sig.sig)
		fmt.Fprintf(&b, "\t// TODO: "+sig.body+"\n", svcFn)
		b.WriteString("\treturn 0, 0, nil\n}\n")
	}
	// gofmt the result so the emitted file is drop-in clean. On the (unexpected)
	// event of a syntax error, return the raw source so the human can debug it.
	if formatted, err := format.Source([]byte(b.String())); err == nil {
		return string(formatted)
	}
	return b.String()
}

// resourceSegment extracts the resource portion of a provider-specific upstream
// key: AWS "AWS::Svc::Resource" -> "Resource"; GCP "api.googleapis.com/Resource"
// and Azure "Microsoft.X/types/child" -> the last path segment.
func resourceSegment(provName, key string) string {
	switch provName {
	case "aws":
		parts := strings.Split(key, "::")
		return parts[len(parts)-1]
	default:
		parts := strings.Split(key, "/")
		return parts[len(parts)-1]
	}
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
