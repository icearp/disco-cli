package azure

// azTagsJSON converts an Azure SDK tag map to a JSON-encoded {key:value} string pointer.
// Returns nil when tags is nil or empty.
func azTagsJSON(tags map[string]*string) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			m[k] = *v
		}
	}
	s := mustJSON(m)
	return &s
}
