package aws

import (
	"strings"
	"testing"

	"github.com/icearp/disco-cli/store"
)

// volatileReason is why a type is allowed to drop an attribute. Dropping is
// the exception -- disco records what the provider reported -- so every rule
// must name one of these two, and each carries its own admission test below.
type volatileReason string

const (
	// reasonRotating: the provider returns a fresh value on every read
	// independent of any real change, so storing it version-splits an
	// unchanged resource on every scan.
	reasonRotating volatileReason = "rotating"

	// reasonRegionCollision: the type's ARN carries no region, so every region
	// reports the same natural key while the field differs per region. Only
	// admissible for a genuinely region-less ARN -- with a region-qualified
	// ARN each region gets its own row and the field must be kept.
	reasonRegionCollision volatileReason = "region-collision"
)

// volatilePolicy is the complete allowlist of AWS types permitted to drop
// attributes. A type absent here must register no volatile paths.
var volatilePolicy = map[string]struct {
	reason volatileReason
	paths  []string
	// sampleARN is required for reasonRegionCollision: its region segment is
	// asserted empty, which is the whole justification.
	sampleARN string
}{
	TypeLogsLogStream: {
		reason: reasonRotating,
		paths:  []string{"UploadSequenceToken"},
	},
	TypePersonalizeRecipe: {
		reason:    reasonRegionCollision,
		paths:     []string{"LastUpdatedDateTime"},
		sampleARN: "arn:aws:personalize:::recipe/aws-ecomm-customers-who-viewed-x-viewed-y",
	},
	TypeWellArchitectedLens: {
		reason:    reasonRegionCollision,
		paths:     []string{"CreatedAt", "UpdatedAt"},
		sampleARN: "arn:aws:wellarchitected::aws:lens/connectedmobility",
	},
}

// TestVolatileRulesAreJustified holds attribute-dropping to the allowlist
// above. It fails when a descriptor registers a volatile path that
// volatilePolicy does not admit, which forces the reason to be stated rather
// than assumed -- and, for the region-collision reason, forces the ARN to
// actually be region-less.
func TestVolatileRulesAreJustified(t *testing.T) {
	declared := map[string][]string{}
	for _, d := range registeredDescriptors {
		if len(d.Volatile) > 0 {
			declared[d.Type] = append(declared[d.Type], d.Volatile...)
		}
	}

	for resType, paths := range declared {
		rule, ok := volatilePolicy[resType]
		if !ok {
			t.Errorf("%s drops attributes %v with no entry in volatilePolicy; "+
				"an attribute is stored as the provider reported it unless a "+
				"stated reason says otherwise", resType, paths)
			continue
		}
		if !equalStringSets(paths, rule.paths) {
			t.Errorf("%s drops %v; volatilePolicy admits only %v", resType, paths, rule.paths)
		}
		if rule.reason == reasonRegionCollision {
			assertRegionlessARN(t, resType, rule.sampleARN)
		}
	}

	for resType, rule := range volatilePolicy {
		if _, ok := declared[resType]; !ok {
			t.Errorf("volatilePolicy admits %s (%s) but no descriptor registers it; "+
				"drop the entry rather than leaving a standing permission",
				resType, rule.reason)
		}
	}
}

// assertRegionlessARN fails unless arn's region segment is empty, which is the
// only thing that makes reasonRegionCollision true: a region-qualified ARN
// gives each region its own row, so the differing field is real per-row data.
func assertRegionlessARN(t *testing.T, resType, arn string) {
	t.Helper()
	if arn == "" {
		t.Errorf("%s claims %s but supplies no sample ARN to check", resType, reasonRegionCollision)
		return
	}
	// arn:partition:service:region:account:resource
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		t.Errorf("%s sample ARN %q is not an ARN", resType, arn)
		return
	}
	if parts[3] != "" {
		t.Errorf("%s claims %s but its ARN %q is region-qualified (region=%q); "+
			"each region gets its own row, so the field must be kept",
			resType, reasonRegionCollision, arn, parts[3])
	}
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// TestGlobalCatalogTypes_DoNotSplitPerRegion is the behavioural half: the
// region-collision rules must actually collapse the regions into one row.
// Without them a single scan appends one junk version per region and the
// surviving row is decided by commit order.
func TestGlobalCatalogTypes_DoNotSplitPerRegion(t *testing.T) {
	cases := []struct {
		resType   string
		nativeID  string
		perRegion func(stamp string) string
	}{
		{
			resType:  TypePersonalizeRecipe,
			nativeID: "arn:aws:personalize:::recipe/aws-ecomm-customers-who-viewed-x-viewed-y",
			perRegion: func(stamp string) string {
				return `{"RecipeArn":"arn:aws:personalize:::recipe/aws-ecomm","LastUpdatedDateTime":"` + stamp + `"}`
			},
		},
		{
			resType:  TypeWellArchitectedLens,
			nativeID: "arn:aws:wellarchitected::aws:lens/connectedmobility",
			perRegion: func(stamp string) string {
				return `{"LensName":"Connected Mobility","CreatedAt":"` + stamp + `","UpdatedAt":"` + stamp + `"}`
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.resType, func(t *testing.T) {
			st := newTestStore(t)
			for _, stamp := range []string{"2021-01-01T00:00:00Z", "2023-06-15T12:00:00Z", "2024-11-02T08:30:00Z"} {
				region := "r" + stamp[:4]
				if _, err := st.UpsertResources([]*store.Resource{{
					Provider: "aws", AccountID: testAccountID, Type: tc.resType,
					NativeID: tc.nativeID, Region: &region,
					AttributesJSON: tc.perRegion(stamp), DiscoveredBy: testScanID,
				}}); err != nil {
					t.Fatalf("upsert %s: %v", stamp, err)
				}
			}

			var versions int
			if err := st.DB().QueryRow(
				`SELECT count(*) FROM resources WHERE native_id = ?`, tc.nativeID,
			).Scan(&versions); err != nil {
				t.Fatalf("count versions: %v", err)
			}
			if versions != 1 {
				t.Errorf("%d rows for one AWS-owned catalog entry; want 1 — each extra "+
					"row is a region's rollout timestamp masquerading as a change", versions)
			}
		})
	}
}
