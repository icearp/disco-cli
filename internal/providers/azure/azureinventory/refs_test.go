package azureinventory

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestParseResults_HeadOpDoesNotStealNextResult: a HEAD op's decoder carries
// no &result.X. Unbounded, the regex ran on past it into the next function and
// took that function's result type, and because matches cannot overlap the
// real lister was then left with none — 106 listers lost their refs.
func TestParseResults_HeadOpDoesNotStealNextResult(t *testing.T) {
	src := `package armwidgets

func (client *WidgetsClient) getEntityTagHandleResponse(resp *http.Response) (WidgetsClientGetEntityTagResponse, error) {
	result := WidgetsClientGetEntityTagResponse{Success: resp.StatusCode >= 200}
	if etag := resp.Header.Get("ETag"); etag != "" {
		result.ETag = &etag
	}
	return result, nil
}

func (client *WidgetsClient) listHandleResponse(resp *http.Response) (WidgetsClientListResponse, error) {
	result := WidgetsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetListResult); err != nil {
		return WidgetsClientListResponse{}, err
	}
	return result, nil
}
`
	got := parseResults(src)
	if got["List"] != "WidgetListResult" {
		t.Errorf("List result = %q, want WidgetListResult", got["List"])
	}
	if r, ok := got["GetEntityTag"]; ok {
		t.Errorf("HEAD op claimed result %q", r)
	}
}

// TestLoadModels_LegacyLayoutAndResponseArrays: the older generator emits
// zz_generated_models.go with struct tags, and decodes a list into a bare
// <X>Array field of the response struct, which models.go never declares.
func TestLoadModels_LegacyLayoutAndResponseArrays(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sdk", "resourcemanager", "old", "armold")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zz_generated_models.go", `package armold

type Rollout struct {
	// The artifact source.
	Properties *RolloutProperties `+"`json:\"properties,omitempty\"`"+`

	// READ-ONLY; Resource ID.
	ID *string `+"`json:\"id,omitempty\"`"+`
}

type RolloutProperties struct {
	// The artifact source.
	ArtifactSourceID *string `+"`json:\"artifactSourceId,omitempty\"`"+`
}
`)
	write("response_types.go", `package armold

// RolloutsClientListResponse contains the response from method RolloutsClient.List.
type RolloutsClientListResponse struct {
	// The list of rollouts.
	RolloutArray []*Rollout
}
`)
	m := loadModels(root, "sdk/resourcemanager/old/armold")
	if f := m["Rollout"]; len(f) != 2 || f[0].typ != "*RolloutProperties" {
		t.Fatalf("Rollout fields = %+v (struct tag not cut?)", f)
	}
	if got := refsOf(m, "RolloutArray"); !slices.Equal(got, []string{"properties.artifactSourceID"}) {
		t.Errorf("refsOf(RolloutArray) = %v", got)
	}
}

func TestIsStringRef(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		want       bool
	}{
		{"SubnetID", "properties.subnetID", true},
		{"ManagedBy", "managedBy", true},
		{"KeyVaultURI", "properties.keyVaultURI", true},
		{"KeyEncryptionKeyURL", "properties.encryptionKey.keyEncryptionKeyURL", true},
		{"SignInURL", "properties.signInURL", false},
		{"Endpoint", "properties.endpoint", false},
		{"ManagedByExtended", "managedByExtended", false},
	} {
		if got := isStringRef(tc.name, tc.path); got != tc.want {
			t.Errorf("isStringRef(%q, %q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}
