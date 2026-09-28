package azureinventory

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Generated models.go: every struct is `type X struct {` with one exported
// field per line; comments and blank lines in between.
var (
	structRe = regexp.MustCompile(`(?m)^type (\w+) struct \{\n((?:.*\n)*?)\}`)
	fieldRe  = regexp.MustCompile(`(?m)^\t([A-Z]\w*) (.+)$`)
	// handleHeadRe finds an operation's response decoder; resultRe finds the
	// list-result field it unmarshals into, searched only within that
	// decoder's own body (topFuncRe bounds it).
	handleHeadRe = regexp.MustCompile(`(?m)^func \(client \*\w*Client\) (\w+)HandleResponse\(`)
	resultRe     = regexp.MustCompile(`&result\.(\w+)\)`)
	topFuncRe    = regexp.MustCompile(`(?m)^func `)
)

type field struct{ name, typ string }

// models is one module's struct table, parsed on first use.
type models map[string][]field

// arrayFieldRe matches a response struct's bare list field ("RolloutArray
// []*Rollout"), the decode shape used by the older generator instead of a
// named ...ListResult model.
var arrayFieldRe = regexp.MustCompile(`^\[\]\*(\w+)$`)

// loadModels parses one module's struct table. Two generator layouts exist:
// models.go with no struct tags, and the older zz_generated_models.go whose
// fields carry a `json:"..."` tag — hence the cut at the first backtick.
// response_types.go joins the same table because the array decode shape names
// a field of the response struct, which is declared nowhere else.
func loadModels(root, module string) models {
	dir := filepath.Join(root, filepath.FromSlash(module))
	out := models{}
	for _, name := range []string{"models.go", "zz_generated_models.go"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, m := range structRe.FindAllStringSubmatch(string(raw), -1) {
			var fields []field
			for _, f := range fieldRe.FindAllStringSubmatch(m[2], -1) {
				typ := strings.TrimSpace(f[2])
				if i := strings.IndexByte(typ, '`'); i >= 0 {
					typ = strings.TrimSpace(typ[:i])
				}
				fields = append(fields, field{f[1], typ})
			}
			out[m[1]] = fields
		}
	}
	if len(out) == 0 {
		return nil
	}
	addResponseArrays(dir, out)
	return out
}

// addResponseArrays registers each response struct's "<X>Array []*X" field as
// a synthetic list-result model, so refsOf resolves it the same way it
// resolves a real ...ListResult. A name the module already declares wins.
func addResponseArrays(dir string, out models) {
	raw, err := os.ReadFile(filepath.Join(dir, "response_types.go"))
	if err != nil {
		return
	}
	for _, m := range structRe.FindAllStringSubmatch(string(raw), -1) {
		for _, f := range fieldRe.FindAllStringSubmatch(m[2], -1) {
			name, typ := f[1], strings.TrimSpace(f[2])
			if i := strings.IndexByte(typ, '`'); i >= 0 {
				typ = strings.TrimSpace(typ[:i])
			}
			if !strings.HasSuffix(name, "Array") || !arrayFieldRe.MatchString(typ) {
				continue
			}
			if _, dup := out[name]; !dup {
				out[name] = []field{{name: "Value", typ: typ}}
			}
		}
	}
}

// parseResults maps each operation to the result type its response decoder
// fills (listAllHandleResponse → VirtualMachineListResult).
func parseResults(src string) map[string]string {
	// Each decoder's body ends at the next top-level func. Without that bound
	// a HEAD op's tag-only decoder (no &result.X at all) swallowed the next
	// function and stole its result type, and because the matches cannot
	// overlap the real lister then got none — 106 listers lost their refs.
	funcs := topFuncRe.FindAllStringIndex(src, -1)
	out := map[string]string{}
	for _, loc := range handleHeadRe.FindAllStringSubmatchIndex(src, -1) {
		end := len(src)
		for _, f := range funcs {
			if f[0] > loc[0] {
				end = f[0]
				break
			}
		}
		if m := resultRe.FindStringSubmatch(src[loc[0]:end]); m != nil {
			out[upperFirst(src[loc[2]:loc[3]])] = m[1]
		}
	}
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// refDepth bounds the walk below the listed element:
// properties.networkProfile.networkInterfaces is a ref, deeper is detail.
const refDepth = 3

// ownFields are the ARM envelope's own identity and bookkeeping.
var ownFields = map[string]bool{"ID": true, "Name": true, "Type": true, "Location": true, "Tags": true, "Etag": true, "SystemData": true, "Kind": true}

// refsOf lists the fields on a list result's element that reference other
// ARM resources: a sub-resource struct (only an ID, or named *Reference /
// *SubResource) or an ID-suffixed string, at any depth up to refDepth.
func refsOf(m models, listResult string) []string {
	element := elementOf(m, listResult)
	if element == "" {
		return nil
	}
	refs := map[string]bool{}
	walkRefs(m, element, "", 0, refs)
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

// armEnvelope reports whether a model carries the ARM proxy-resource envelope:
// SystemData, or both ID and Type. It is what separates a listing of resources
// the subscription owns from a published catalog of the provider's own data.
func armEnvelope(m models, element string) bool {
	if element == "" {
		return false
	}
	var hasID, hasType bool
	for _, f := range m[element] {
		switch f.name {
		case "SystemData":
			return true
		case "ID":
			hasID = true
		case "Type":
			hasType = true
		}
	}
	return hasID && hasType
}

// elementOf names the model a list result enumerates. Two listers that
// enumerate the same element under different parents list the same resource.
func elementOf(m models, listResult string) string {
	for _, f := range m[listResult] {
		if f.name == "Value" && strings.HasPrefix(f.typ, "[]*") {
			return f.typ[3:]
		}
	}
	return ""
}

func walkRefs(m models, typ, prefix string, depth int, refs map[string]bool) {
	for _, f := range m[typ] {
		if depth == 0 && ownFields[f.name] {
			continue
		}
		path := prefix + lowerFirst(f.name)
		base := strings.TrimLeft(f.typ, "[]*")
		switch {
		case isRefStruct(m, base):
			refs[path] = true
		case f.typ == "*string" || f.typ == "[]*string":
			if isStringRef(f.name, path) {
				refs[path] = true
			}
		case depth < refDepth && len(m[base]) > 0 && !strings.HasPrefix(f.typ, "map["):
			walkRefs(m, base, path+".", depth+1, refs)
		}
	}
}

// isStringRef decides whether a string field names another ARM resource.
// ID-suffixed names are the bulk; ManagedBy is the exact name ARM uses for the
// owning resource's id. The URI/URL family is admitted only under a KeyVault
// or encryption-key path — a bare URI/URL suffix pulls in a hundred data-plane
// endpoints and sign-on URLs naming no ARM resource, while these are the
// strings three Azure resolvers already parse (vaultNameFromVaultURI).
func isStringRef(name, path string) bool {
	if name == "ManagedBy" {
		return true
	}
	if strings.HasSuffix(name, "ID") || strings.HasSuffix(name, "IDs") {
		return true
	}
	if !uriSuffixRe.MatchString(name) {
		return false
	}
	lower := strings.ToLower(path)
	return strings.Contains(lower, "keyvault") || strings.Contains(lower, "encryptionkey")
}

var uriSuffixRe = regexp.MustCompile(`(?i)ur[il]s?$`)

// isRefStruct: a struct that is nothing but a pointer to another resource.
func isRefStruct(m models, typ string) bool {
	fields, ok := m[typ]
	if !ok {
		return false
	}
	if strings.HasSuffix(typ, "SubResource") || strings.HasSuffix(typ, "Reference") {
		return true
	}
	hasID := false
	for _, f := range fields {
		if f.name == "ID" && f.typ == "*string" {
			hasID = true
		}
	}
	return hasID && len(fields) <= 2
}

func lowerFirst(s string) string {
	// ARM JSON names are lowerCamel; leading initialisms (ID, DNSName) lower entirely up to the last capital of the run.
	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		i++
	}
	if i > 1 && i < len(s) {
		i--
	}
	return strings.ToLower(s[:i]) + s[i:]
}
