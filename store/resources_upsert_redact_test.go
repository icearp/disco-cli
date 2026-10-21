package store

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/icearp/disco-cli/internal/redact"
)

// testRedactType carries a redaction rule; testNoRulesType carries none.
const (
	testRedactType  = "test:redact:with-rules"
	testNoRulesType = "test:redact:no-rules"
	testSecret      = "hunter2-secret"
)

var registerTestRedactRule = sync.OnceFunc(func() {
	redact.Register(redact.TypeRules{
		Type:       testRedactType,
		Attributes: []redact.Rule{{Path: "Password", Mode: redact.RedactScalar}},
	})
})

// resourceWriters are the two store entry points that run redact.Apply.
var resourceWriters = []struct {
	name  string
	write func(*Store, []*Resource) (int, error)
}{
	{"UpsertResources", (*Store).UpsertResources},
	{"InsertResourcesIfAbsent", (*Store).InsertResourcesIfAbsent},
}

// writeAndCollect writes one resource through write, returning the stored row
// and every ScanError the write reported.
func writeAndCollect(t *testing.T, st *Store, write func(*Store, []*Resource) (int, error), typ, nativeID, attrs string) (*Resource, []ScanError) {
	t.Helper()
	var errs []ScanError
	st.OnError = func(e ScanError) { errs = append(errs, e) }
	t.Cleanup(func() { st.OnError = nil })
	r := &Resource{
		Provider: "aws", AccountID: "111", Type: typ, NativeID: nativeID,
		AttributesJSON: attrs, DiscoveredBy: testScanID,
	}
	if _, err := write(st, []*Resource{r}); err != nil {
		t.Fatalf("write %s: %v", nativeID, err)
	}
	got, err := st.GetResource(ResourceID("aws", "111", nativeID))
	if err != nil {
		t.Fatalf("GetResource(%s): %v", nativeID, err)
	}
	return got, errs
}

func TestResourceWriters_MalformedAttributesOnRuledTypeFailClosed(t *testing.T) {
	registerTestRedactRule()
	for _, w := range resourceWriters {
		t.Run(w.name, func(t *testing.T) {
			withDialects(t, func(t *testing.T, st *Store) {
				// The secret is unquoted, so parsing stops inside it.
				in := `{"Password":` + testSecret + `}`
				got, errs := writeAndCollect(t, st, w.write, testRedactType, "malformed-1", in)

				var attrs, want map[string]any
				if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil {
					t.Fatalf("stored attributes %q are not JSON: %v", got.AttributesJSON, err)
				}
				if err := json.Unmarshal([]byte(redact.UnparseableAttributes), &want); err != nil {
					t.Fatalf("placeholder: %v", err)
				}
				if attrs["redacted"] != want["redacted"] || len(attrs) != len(want) {
					t.Errorf("stored attributes = %v; want %v", attrs, want)
				}
				if strings.Contains(got.AttributesJSON, testSecret) {
					t.Errorf("stored attributes leak the secret: %s", got.AttributesJSON)
				}
				if len(errs) != 1 {
					t.Fatalf("scan errors = %d (%+v); want 1", len(errs), errs)
				}
				e := errs[0]
				if e.Provider != "aws" || e.Service != testRedactType || e.Scope != "111" {
					t.Errorf("scan error = {%s %s %s}; want {aws %s 111}", e.Provider, e.Service, e.Scope, testRedactType)
				}
				if strings.Contains(e.Message, testSecret) {
					t.Errorf("scan error leaks the secret: %s", e.Message)
				}
			})
		})
	}
}

func TestResourceWriters_WellFormedAttributesOnRuledTypeRedacted(t *testing.T) {
	registerTestRedactRule()
	for _, w := range resourceWriters {
		t.Run(w.name, func(t *testing.T) {
			withDialects(t, func(t *testing.T, st *Store) {
				in := `{"Password":"` + testSecret + `","Keep":"y"}`
				got, errs := writeAndCollect(t, st, w.write, testRedactType, "well-formed-1", in)

				var attrs map[string]any
				if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil {
					t.Fatalf("stored attributes %q are not JSON: %v", got.AttributesJSON, err)
				}
				if attrs["Password"] != redact.Placeholder || attrs["Keep"] != "y" || len(attrs) != 2 {
					t.Errorf("stored attributes = %v; want Password redacted, Keep kept", attrs)
				}
				if len(errs) != 0 {
					t.Errorf("scan errors = %+v; want none", errs)
				}
			})
		})
	}
}

// With no rules there is nothing to redact, so even unparseable attributes are
// stored as given. SQLite only: Postgres stores attributes as JSONB and refuses
// the malformed value itself.
func TestResourceWriters_MalformedAttributesOnUnruledTypeStoredRaw(t *testing.T) {
	for _, w := range resourceWriters {
		t.Run(w.name, func(t *testing.T) {
			st := openTestStore(t)
			in := `{"Password":"` + testSecret + `"`
			got, errs := writeAndCollect(t, st, w.write, testNoRulesType, "no-rules-1", in)
			if got.AttributesJSON != in {
				t.Errorf("stored attributes = %q; want %q", got.AttributesJSON, in)
			}
			if len(errs) != 0 {
				t.Errorf("scan errors = %+v; want none", errs)
			}
		})
	}
}
