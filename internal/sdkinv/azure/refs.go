package azure

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
	// handleRe finds the operation's response decoder and the list-result
	// type it unmarshals into.
	handleRe = regexp.MustCompile(`(?s)func \(client \*\w*Client\) (\w+)HandleResponse\(.*?&result\.(\w+)\)`)
)

type field struct{ name, typ string }

// models is one module's struct table, parsed on first use.
type models map[string][]field

func loadModels(root, module string) models {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(module), "models.go"))
	if err != nil {
		return nil
	}
	out := models{}
	for _, m := range structRe.FindAllStringSubmatch(string(raw), -1) {
		var fields []field
		for _, f := range fieldRe.FindAllStringSubmatch(m[2], -1) {
			fields = append(fields, field{f[1], f[2]})
		}
		out[m[1]] = fields
	}
	return out
}

// parseResults maps each operation to the result type its response decoder
// fills (listAllHandleResponse → VirtualMachineListResult).
func parseResults(src string) map[string]string {
	out := map[string]string{}
	for _, m := range handleRe.FindAllStringSubmatch(src, -1) {
		out[upperFirst(m[1])] = m[2]
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
	var element string
	for _, f := range m[listResult] {
		if f.name == "Value" && strings.HasPrefix(f.typ, "[]*") {
			element = f.typ[3:]
		}
	}
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
			if strings.HasSuffix(f.name, "ID") || strings.HasSuffix(f.name, "IDs") || strings.HasSuffix(f.name, "ResourceID") {
				refs[path] = true
			}
		case depth < refDepth && len(m[base]) > 0 && !strings.HasPrefix(f.typ, "map["):
			walkRefs(m, base, path+".", depth+1, refs)
		}
	}
}

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
