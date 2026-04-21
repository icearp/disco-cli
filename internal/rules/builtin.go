package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// Builtins returns the rules embedded at compile time. Source is tagged as
// "<builtin>:<filename>" so findings and duplicate-id errors point at the file.
func Builtins() ([]Rule, error) {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil, fmt.Errorf("read builtins: %w", err)
	}
	// Sort for deterministic iteration so embedded rule order is stable
	// across builds regardless of filesystem ordering.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var out []Rule
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(builtinFS, "builtin/"+e.Name())
		if err != nil {
			return nil, err
		}
		src := "<builtin>:" + e.Name()
		rules, err := parseRules(data, src)
		if err != nil {
			return nil, err
		}
		for _, r := range rules {
			if prev, dup := seen[r.ID]; dup {
				return nil, fmt.Errorf("duplicate builtin rule id %q (%s and %s)", r.ID, prev, r.Source)
			}
			seen[r.ID] = r.Source
			out = append(out, r)
		}
	}
	return out, nil
}
