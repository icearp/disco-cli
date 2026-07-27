package aws

import (
	"testing"

	"github.com/icearp/disco-cli/internal/volatile"
	"github.com/icearp/disco-cli/store"
)

// TestGlobalCatalogTypes_DoNotSplitPerRegion pins that an AWS-owned catalog
// entry stays one resource across every region that reports it.
//
// These ARNs carry no region and no account (arn:aws:personalize:::recipe/...,
// arn:aws:wellarchitected::aws:lens/...), so AWS itself declares them
// partition-global and one natural key is correct. What is NOT the same across
// regions is AWS's own rollout timestamp, and under a region-less key that is
// indistinguishable from the resource changing: each region's scanner
// version-split the previous one, so a single scan appended one junk version
// per region and the surviving row was decided by commit order.
//
// The timestamps are registered volatile, which drops them before the
// attribute comparison. Note this is a different reason from the usual one --
// they do not rotate on every read, they are stable per region and differ
// across regions. The store cannot tell those apart, which is why the same
// mechanism fits.
func TestGlobalCatalogTypes_DoNotSplitPerRegion(t *testing.T) {
	cases := []struct {
		resType  string
		nativeID string
		// perRegion returns the attribute payload one region would report.
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
			if !volatile.HasRules(tc.resType) {
				t.Fatalf("%s registers no volatile paths; every region's rollout "+
					"timestamp will version-split the shared natural key", tc.resType)
			}
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
