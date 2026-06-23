package azure

// azTagsJSON serializes an Azure SDK tag map to a JSON string pointer,
// faithfully: it preserves null values and serializes an empty non-nil map as
// "{}", returning nil only for a genuinely nil map. disco stores the API
// response as-is rather than reshaping it. mustJSON sorts map keys, so the
// output is deterministic.
func azTagsJSON(tags map[string]*string) *string {
	if tags == nil {
		return nil
	}
	s := mustJSON(tags)
	return &s
}
