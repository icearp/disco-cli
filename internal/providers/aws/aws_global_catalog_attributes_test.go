package aws

import (
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/volatile"
	"github.com/icearp/disco-cli/store"
)

// TestGlobalCatalogTypes_StoreAttributesVerbatim pins that disco records what
// the provider reported, for the two types where dropping a field would be
// most tempting.
//
// An AWS-owned catalog ARN carries no region and no account
// (arn:aws:personalize:::recipe/..., arn:aws:wellarchitected::aws:lens/...),
// so AWS declares it partition-global and every region reports the same
// natural key. AWS's per-region rollout stamps differ, so those regions
// version-split each other within one scan. Registering the stamps volatile
// would end that churn by deleting the keys before the comparison — which
// trades a real reported value for tidier history, and the value wins. The
// churn is bounded: the extra versions are superseded, and resource_count
// counts current rows only, so it inflates no total.
//
// This test exists to fail if that trade is ever quietly made.
func TestGlobalCatalogTypes_StoreAttributesVerbatim(t *testing.T) {
	cases := []struct {
		resType  string
		nativeID string
		attrs    string
		// keep names the fields a volatile rule would have removed.
		keep []string
	}{
		{
			resType:  TypePersonalizeRecipe,
			nativeID: "arn:aws:personalize:::recipe/aws-ecomm-customers-who-viewed-x-viewed-y",
			attrs:    `{"RecipeArn":"arn:aws:personalize:::recipe/aws-ecomm","LastUpdatedDateTime":"2024-11-02T08:30:00Z"}`,
			keep:     []string{"LastUpdatedDateTime"},
		},
		{
			resType:  TypeWellArchitectedLens,
			nativeID: "arn:aws:wellarchitected::aws:lens/connectedmobility",
			attrs:    `{"LensName":"Connected Mobility","CreatedAt":"2021-01-01T00:00:00Z","UpdatedAt":"2023-06-15T12:00:00Z"}`,
			keep:     []string{"CreatedAt", "UpdatedAt"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.resType, func(t *testing.T) {
			if volatile.HasRules(tc.resType) {
				t.Errorf("%s registers volatile paths; a scanned attribute must reach "+
					"the store as the provider reported it", tc.resType)
			}

			st := newTestStore(t)
			region := "us-east-2"
			if _, err := st.UpsertResources([]*store.Resource{{
				Provider: "aws", AccountID: testAccountID, Type: tc.resType,
				NativeID: tc.nativeID, Region: &region,
				AttributesJSON: tc.attrs, DiscoveredBy: testScanID,
			}}); err != nil {
				t.Fatalf("upsert: %v", err)
			}

			var stored string
			if err := st.DB().QueryRow(
				`SELECT attributes FROM resources WHERE native_id = ? AND superseded_by IS NULL`,
				tc.nativeID,
			).Scan(&stored); err != nil {
				t.Fatalf("read back attributes: %v", err)
			}
			for _, k := range tc.keep {
				if !strings.Contains(stored, k) {
					t.Errorf("attribute %q missing from stored payload %s", k, stored)
				}
			}
		})
	}
}
