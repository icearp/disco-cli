package rules

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"github.com/tidwall/gjson"
)

// Evaluate runs every rule whose severity meets minSev against the store and
// returns a flat findings list. Rules short-circuit per resource: the first
// predicate that fails drops the resource from that rule's result.
//
// Coarse filters (provider/type/region) push into ListResources; attribute
// predicates run in Go against the already-persisted JSON blob.
func Evaluate(st *store.Store, rules []Rule, minSev Severity) ([]Finding, error) {
	var findings []Finding
	for _, r := range rules {
		if minSev != "" && !r.Severity.AtLeast(minSev) {
			continue
		}
		filter := store.ResourceFilter{
			Provider: r.Match.Provider,
			Limit:    util.AllResources,
		}
		if r.Match.Type != "" {
			filter.Types = []string{r.Match.Type}
		}
		if r.Match.Region != "" {
			filter.Regions = []string{r.Match.Region}
		}
		resources, err := st.ListResources(filter)
		if err != nil {
			return nil, fmt.Errorf("rule %s: list: %w", r.ID, err)
		}
		for i := range resources {
			res := &resources[i]
			if !matchesAll(r.Match.Where, res.AttributesJSON) {
				continue
			}
			msg := r.Description
			if msg == "" {
				msg = r.ID
			}
			findings = append(findings, Finding{
				RuleID:     r.ID,
				Severity:   r.Severity,
				ResourceID: res.ID,
				Provider:   res.Provider,
				Type:       res.Type,
				Name:       res.Name,
				Region:     res.Region,
				Message:    msg,
			})
		}
	}
	return findings, nil
}

// matchesAll returns true when every predicate holds against attrs.
func matchesAll(preds []Predicate, attrs string) bool {
	for _, p := range preds {
		if !evalPredicate(p, attrs) {
			return false
		}
	}
	return true
}

// evalPredicate resolves p.Path against attrs using gjson and applies p.Op.
// Paths that fan out via `#` yield a gjson array; most ops are satisfied if
// *any* yielded value matches, matching the intuitive reading of rules like
// "any ingress rule contains 0.0.0.0/0".
func evalPredicate(p Predicate, attrs string) bool {
	res := gjson.Get(attrs, p.Path)
	switch p.Op {
	case "exists":
		return res.Exists() && anyTrue(res, func(r gjson.Result) bool { return r.Type != gjson.Null })
	case "not_exists":
		if !res.Exists() {
			return true
		}
		return !anyTrue(res, func(r gjson.Result) bool { return r.Type != gjson.Null })
	case "eq":
		return anyTrue(res, func(r gjson.Result) bool { return equalValue(r, p.Value) })
	case "ne":
		if !res.Exists() {
			return true
		}
		return !anyTrue(res, func(r gjson.Result) bool { return equalValue(r, p.Value) })
	case "contains":
		needle, _ := p.Value.(string)
		return anyTrue(res, func(r gjson.Result) bool {
			return strings.Contains(r.String(), needle)
		})
	case "matches":
		if p.Regex == nil {
			return false
		}
		return anyTrue(res, func(r gjson.Result) bool { return p.Regex.MatchString(r.String()) })
	}
	return false
}

// anyTrue applies fn to each element of res. gjson returns arrays for `#`
// fan-out paths; scalars are wrapped in a one-element walk.
func anyTrue(res gjson.Result, fn func(gjson.Result) bool) bool {
	if !res.Exists() {
		return false
	}
	if res.IsArray() {
		hit := false
		res.ForEach(func(_, v gjson.Result) bool {
			if fn(v) {
				hit = true
				return false
			}
			return true
		})
		return hit
	}
	return fn(res)
}

// equalValue compares a gjson result to a YAML-decoded value. YAML unmarshals
// booleans/ints/floats/strings into typed `any`, so we dispatch on the YAML
// side and coerce the gjson side accordingly.
func equalValue(r gjson.Result, want any) bool {
	switch w := want.(type) {
	case bool:
		return r.Type == gjson.True && w || r.Type == gjson.False && !w
	case string:
		return r.Type == gjson.String && r.String() == w
	case int:
		return r.Type == gjson.Number && r.Int() == int64(w)
	case int64:
		return r.Type == gjson.Number && r.Int() == w
	case float64:
		return r.Type == gjson.Number && r.Float() == w
	case nil:
		return r.Type == gjson.Null || !r.Exists()
	}
	// Fallback: compare raw JSON text.
	return r.Raw == fmt.Sprint(want)
}
