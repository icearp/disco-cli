package azure

// azTagsJSON serializes an Azure SDK tag map to a JSON string pointer:
// preserves null values, renders an empty non-nil map as "{}", and returns
// nil only for a genuinely nil map. disco stores the API response as-is, not
// reshaped. mustJSON sorts keys, so output is deterministic.
func azTagsJSON(tags map[string]*string) *string {
	if tags == nil {
		return nil
	}
	s := mustJSON(tags)
	return &s
}
