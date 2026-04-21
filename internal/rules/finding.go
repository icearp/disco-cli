package rules

// Finding is a single rule violation attached to a specific resource.
type Finding struct {
	RuleID     string   `json:"rule_id"`
	Severity   Severity `json:"severity"`
	ResourceID string   `json:"resource_id"`
	Provider   string   `json:"provider"`
	Type       string   `json:"type"`
	Name       *string  `json:"name,omitempty"`
	Region     *string  `json:"region,omitempty"`
	Message    string   `json:"message"`
}
