package rules

import "slices"

// Finding is a single rule violation attached to a specific resource.
type Finding struct {
	RuleID      string              `json:"rule_id"`
	Severity    Severity            `json:"severity"`
	ResourceID  string              `json:"resource_id"`
	Provider    string              `json:"provider"`
	Type        string              `json:"type"`
	Name        *string             `json:"name,omitempty"`
	Region      *string             `json:"region,omitempty"`
	Message     string              `json:"message"`
	Tags        map[string][]string `json:"tags,omitempty"`
	Category    string              `json:"category,omitempty"`
	Remediation string              `json:"remediation,omitempty"`
	RefURL      string              `json:"ref_url,omitempty"`
}

// HasTag reports whether the finding carries a tag matching key=value.
// Empty value matches any value under key (presence check). Empty key
// matches any tag.
func (f *Finding) HasTag(key, value string) bool {
	if key == "" {
		return len(f.Tags) > 0
	}
	vals, ok := f.Tags[key]
	if !ok {
		return false
	}
	if value == "" {
		return true
	}
	return slices.Contains(vals, value)
}
