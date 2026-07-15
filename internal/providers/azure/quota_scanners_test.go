package azure

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quota/armquota"
	armquotafake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quota/armquota/fake"
)

func quotaScope(subID, ns, region string) string {
	return "/subscriptions/" + subID + "/providers/" + ns + "/locations/" + region
}

func quotaItem(scope, name string, limit int32) *armquota.CurrentQuotaLimitBase {
	return &armquota.CurrentQuotaLimitBase{
		ID:   to.Ptr(scope + "/providers/Microsoft.Quota/quotas/" + name),
		Name: to.Ptr(name),
		Properties: &armquota.Properties{
			Name:  &armquota.ResourceName{Value: to.Ptr(name)},
			Unit:  to.Ptr("Count"),
			Limit: &armquota.LimitObject{LimitObjectType: to.Ptr(armquota.LimitTypeLimitValue), Value: to.Ptr(limit)},
		},
	}
}

func quotaTestClient(t *testing.T, srv *armquotafake.Server) *armquota.Client {
	t.Helper()
	c, err := armquota.NewClient(fakeCred(), fakeClientOptions(t, armquotafake.NewServerTransport(srv)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// storedLimit unmarshals a stored quota resource's attributes back through the
// SDK's polymorphic decoder and returns the limit value — proves the limit
// survived the mustJSON round-trip the scanner performs.
func storedLimit(t *testing.T, attrs string) int32 {
	t.Helper()
	var base armquota.CurrentQuotaLimitBase
	if err := json.Unmarshal([]byte(attrs), &base); err != nil {
		t.Fatalf("unmarshal stored attrs: %v", err)
	}
	if base.Properties == nil {
		t.Fatalf("stored attrs missing properties: %s", attrs)
	}
	lo, ok := base.Properties.Limit.(*armquota.LimitObject)
	if !ok || lo.Value == nil {
		t.Fatalf("stored attrs missing LimitObject value: %s", attrs)
	}
	return *lo.Value
}

// TestScanQuotaLimits_FakeTransport drives the scanner over a 1-namespace × 2-region
// grid: one region returns two quota limits, the other 403s. Asserts the limits
// land as resources (correct type/region/limit) and the denied region is tolerated
// — it contributes no rows and no error.
func TestScanQuotaLimits_FakeTransport(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	eastScope := quotaScope(sub.ID, "Microsoft.Compute", "eastus")

	server := &armquotafake.Server{
		NewListPager: func(scope string, _ *armquota.ClientListOptions) fake.PagerResponder[armquota.ClientListResponse] {
			r := fake.PagerResponder[armquota.ClientListResponse]{}
			if strings.Contains(scope, "/locations/westus") {
				r.AddResponseError(http.StatusForbidden, "AuthorizationFailed")
				return r
			}
			r.AddPage(http.StatusOK, armquota.ClientListResponse{
				Limits: armquota.Limits{Value: []*armquota.CurrentQuotaLimitBase{
					quotaItem(eastScope, "standardDDv4Family", 100),
					quotaItem(eastScope, "standardDSv5Family", 50),
				}},
			}, nil)
			return r
		},
	}

	total, inserted, err := scanQuotaLimitsWithClient(t.Context(), sub, st, testScanID,
		quotaTestClient(t, server), []string{"Microsoft.Compute"}, []string{"eastus", "westus"})
	if err != nil {
		t.Fatalf("scanQuotaLimitsWithClient: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (westus denied, contributes nothing)", total, inserted)
	}

	id := store.ResourceID("azure", sub.ID, eastScope+"/providers/Microsoft.Quota/quotas/standardDDv4Family")
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Type != TypeQuotaLimit {
		t.Errorf("type: got %q, want %q", got.Type, TypeQuotaLimit)
	}
	if got.Region == nil || *got.Region != "eastus" {
		t.Errorf("region: got %v, want eastus", got.Region)
	}
	if got.Name == nil || *got.Name != "standardDDv4Family" {
		t.Errorf("name: got %v, want standardDDv4Family", got.Name)
	}
	if l := storedLimit(t, got.AttributesJSON); l != 100 {
		t.Errorf("stored limit: got %d, want 100", l)
	}
}

// TestScanQuotaLimits_EmptyRegion guards the len(batch)==0 path: a region that
// returns an empty page produces no rows and no error.
func TestScanQuotaLimits_EmptyRegion(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	server := &armquotafake.Server{
		NewListPager: func(_ string, _ *armquota.ClientListOptions) fake.PagerResponder[armquota.ClientListResponse] {
			r := fake.PagerResponder[armquota.ClientListResponse]{}
			r.AddPage(http.StatusOK, armquota.ClientListResponse{
				Limits: armquota.Limits{Value: nil},
			}, nil)
			return r
		},
	}

	total, inserted, err := scanQuotaLimitsWithClient(t.Context(), sub, st, testScanID,
		quotaTestClient(t, server), []string{"Microsoft.Compute"}, []string{"eastus"})
	if err != nil {
		t.Fatalf("scanQuotaLimitsWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("empty region: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanQuotaLimits_SynthesizedNativeID covers the defensive fallback: when the
// proxy returns no ID, the NativeID is synthesized from scope + the preferred RP
// quota name (Properties.Name.Value), NOT the divergent wrapper Name. Pins the key
// shape so a future change can't silently flip it and split false versions.
func TestScanQuotaLimits_SynthesizedNativeID(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	eastScope := quotaScope(sub.ID, "Microsoft.Compute", "eastus")

	server := &armquotafake.Server{
		NewListPager: func(_ string, _ *armquota.ClientListOptions) fake.PagerResponder[armquota.ClientListResponse] {
			r := fake.PagerResponder[armquota.ClientListResponse]{}
			r.AddPage(http.StatusOK, armquota.ClientListResponse{
				Limits: armquota.Limits{Value: []*armquota.CurrentQuotaLimitBase{{
					// ID absent; wrapper Name deliberately differs from the RP name.
					Name: to.Ptr("wrapper-name"),
					Properties: &armquota.Properties{
						Name:  &armquota.ResourceName{Value: to.Ptr("standardDDv4Family")},
						Limit: &armquota.LimitObject{LimitObjectType: to.Ptr(armquota.LimitTypeLimitValue), Value: to.Ptr(int32(10))},
					},
				}}},
			}, nil)
			return r
		},
	}

	total, _, err := scanQuotaLimitsWithClient(t.Context(), sub, st, testScanID,
		quotaTestClient(t, server), []string{"Microsoft.Compute"}, []string{"eastus"})
	if err != nil {
		t.Fatalf("scanQuotaLimitsWithClient: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	wantID := store.ResourceID("azure", sub.ID, eastScope+"/providers/Microsoft.Quota/quotas/standardDDv4Family")
	if _, err := st.GetResource(wantID); err != nil {
		t.Fatalf("expected resource keyed on synthesized NativeID (RP name): %v", err)
	}
}

// TestScanQuotaLimits_GenuineErrorPropagates proves a non-skippable error (500)
// surfaces to the dispatcher (which reports it) rather than being swallowed
// like the tolerated skippable cases.
func TestScanQuotaLimits_GenuineErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	server := &armquotafake.Server{
		NewListPager: func(_ string, _ *armquota.ClientListOptions) fake.PagerResponder[armquota.ClientListResponse] {
			r := fake.PagerResponder[armquota.ClientListResponse]{}
			r.AddResponseError(http.StatusInternalServerError, "InternalServerError")
			return r
		},
	}

	_, _, err := scanQuotaLimitsWithClient(t.Context(), sub, st, testScanID,
		quotaTestClient(t, server), []string{"Microsoft.Compute"}, []string{"eastus"})
	if err == nil {
		t.Fatal("expected a non-skippable 500 to propagate, got nil")
	}
}

// TestScanQuotaLimits_LimitOnlyChurnFree is the change-over-time contract: storing
// limit-only means a re-scan with an unchanged limit produces NO new version (the
// resource version chain only grows on a real quota change), while a changed limit
// splits a new version. Drives the real scanner + mustJSON serialization so the
// test would also catch the limit response carrying a volatile field.
func TestScanQuotaLimits_LimitOnlyChurnFree(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	eastScope := quotaScope(sub.ID, "Microsoft.Compute", "eastus")

	limit := int32(100)
	server := &armquotafake.Server{
		NewListPager: func(_ string, _ *armquota.ClientListOptions) fake.PagerResponder[armquota.ClientListResponse] {
			r := fake.PagerResponder[armquota.ClientListResponse]{}
			r.AddPage(http.StatusOK, armquota.ClientListResponse{
				Limits: armquota.Limits{Value: []*armquota.CurrentQuotaLimitBase{
					quotaItem(eastScope, "standardDDv4Family", limit),
				}},
			}, nil)
			return r
		},
	}
	client := quotaTestClient(t, server)
	scan := func() {
		t.Helper()
		if _, _, err := scanQuotaLimitsWithClient(t.Context(), sub, st, testScanID,
			client, []string{"Microsoft.Compute"}, []string{"eastus"}); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	rootID := store.ResourceID("azure", sub.ID, eastScope+"/providers/Microsoft.Quota/quotas/standardDDv4Family")
	versionCount := func() int {
		t.Helper()
		v, err := st.GetResourceVersions(rootID)
		if err != nil {
			t.Fatalf("GetResourceVersions: %v", err)
		}
		return len(v)
	}

	scan()
	if n := versionCount(); n != 1 {
		t.Fatalf("after first scan: got %d versions, want 1", n)
	}
	scan() // identical limit → unchanged rescan, no churn
	if n := versionCount(); n != 1 {
		t.Fatalf("after unchanged rescan: got %d versions, want 1 (limit-only must be churn-free)", n)
	}
	limit = 200 // quota granted
	scan()
	versions := func() []store.ResourceVersion {
		v, err := st.GetResourceVersions(rootID)
		if err != nil {
			t.Fatalf("GetResourceVersions: %v", err)
		}
		return v
	}()
	if len(versions) != 2 {
		t.Fatalf("after limit change: got %d versions, want 2", len(versions))
	}
	current, err := st.GetResource(rootID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if l := storedLimit(t, current.AttributesJSON); l != 200 {
		t.Errorf("current limit: got %d, want 200", l)
	}
}
