package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/icearp/disco-cli/store"
)

// refusingCredential fails the test if a token is ever requested.
//
// This is the only way to observe the federation gates from outside: each one
// decides whether a tenant-scope API is CALLED, and a call that never happens
// leaves no trace in the store. Every Azure client takes a credential and asks
// it for a token before its first request, so "no token requested" is exactly
// "no tenant-scope API reached".
type refusingCredential struct{ t *testing.T }

func (c refusingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.t.Helper()
	c.t.Error("a token was requested under a federated credential: a tenant-scope call escaped the gate")
	return azcore.AccessToken{}, errors.New("refused")
}

// TestScanWithCredential_FederatedScanRequestsNoToken is the structural guard
// for every gate in Scan at once.
//
// Under Azure Lighthouse the credential authenticates in DISCO's directory
// while ARM resolves the customer's delegated subscriptions, so a tenant-scope
// call SUCCEEDS and writes disco's own users, groups, service principals and
// management groups into the customer's inventory. Nothing about the scan looks
// wrong afterwards, which is why the gates need a test that fails on the CALL
// rather than on the result.
//
// With no subscriptions the per-subscription fan-out has nothing to do, so any
// token request at all comes from a gated path. Note what this proves and what
// it does not: the tenant SERVICES and stitchTopHierarchy each return early on
// an empty subscription set BEFORE asking for a token, so the refusal here
// covers tenantIDFromCredScope alone. The tenant phase's gate is caught by the
// notice/warning assertions below instead, and the Entities gate by
// TestStitchTopHierarchy_SkipsTheEntitiesCallWithoutTenantScope — NOT by the
// test immediately following, which says at its tail why it deliberately does
// not assert it.
func TestScanWithCredential_FederatedScanRequestsNoToken(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	st := recordingStore(&svcs, &notices, &warnings)

	s := &Scanner{}
	s.scanWithCredential(t.Context(), st, "scan-1", nil, refusingCredential{t: t}, federatedCfg())

	// The same structural claim under a CONSENTED directory. The Entra
	// services run in that mode, so the block in Scan that stamps
	// subscription.tenantID and calls tenantDisplayName looks like it should
	// widen from tenantScopeEnabled to graphTenantEnabled — and must not: both
	// read ARM or describe disco's own directory. Widening it is the most
	// likely next mistake here, and with no subscriptions this is what fails
	// on it, since every tenant service still returns before asking for a
	// token.
	s.scanWithCredential(t.Context(), st, "scan-1b", nil, refusingCredential{t: t}, federatedWithGraph)

	// Assert the suppression is what was reported, not merely that SOMETHING
	// was: with the gates deleted, the failed tenant-id resolution reports its
	// own warning, so a bare count is satisfied by the very failure this test
	// exists to catch.
	// Matched on "skipped:", the one token both KIND-specific notices share.
	// The wording split by kind — an ARM call names no directory, while the
	// Graph service was simply not given one — so a phrase from either branch
	// would pass while the other went unreported.
	reported := false
	for _, n := range notices {
		if strings.HasPrefix(n.Message, directoryLossPrefix) {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the suppressed tenant phase was not reported; an operator cannot tell it from a tenant with no directory objects (notices: %+v, warnings: %+v)", notices, warnings)
	}
}

// discoTenantID stands in for the directory a federated credential speaks for
// — here disco's, the case that motivated the gate. The gate itself does not
// know whose it is, which is why it suppresses unconditionally.
const discoTenantID = "581de929-8709-4154-a686-84103b1adc23"

// tenantStampingCredential mints a decodable token carrying a tid claim.
//
// It must SUCCEED, which is the whole point: under Azure Lighthouse the token
// is real and names disco's tenant, so the ungated code stamps that GUID onto
// the customer's subscriptions. A credential that merely errors proves nothing
// here — tenantIDFromCredScope returns an empty tenant id on any failure, so
// the assertion would pass with the gate deleted.
type tenantStampingCredential struct{}

func (tenantStampingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"` + discoTenantID + `"}`))
	return azcore.AccessToken{
		Token:     "header." + payload + ".signature",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

// TestScanWithCredential_FederatedScanLeavesTenantIDUnstamped guards the
// dependent half. A non-empty subscription.tenantID names the CREDENTIAL's
// tenant, which under Lighthouse is not the scanned customer's and which the
// gate cannot confirm either way, and the per-subscription scanners read it as
// "a tenant service is
// storing the built-in role and policy definitions" — which is precisely what
// the gate switched off. The rows would be dropped from the scan silently.
func TestScanWithCredential_FederatedScanLeavesTenantIDUnstamped(t *testing.T) {
	// A real (SQLite) store: stitchTopHierarchy derives its lower tiers from
	// stored rows, so it runs a query even when the gated Entities call does
	// not — which is itself the documented behaviour under the gate.
	st := newTestStore(t)

	subs := []subscription{{ID: "11111111-1111-1111-1111-111111111111"}}
	s := &Scanner{serviceFilter: []string{"azure:nonexistent"}}
	s.scanWithCredential(t.Context(), st, testScanID, subs, tenantStampingCredential{}, federatedCfg())

	if subs[0].tenantID != "" {
		t.Errorf("subscription.tenantID = %q; want empty — a stamped tenant id names disco's directory and makes the per-sub scanners skip built-ins", subs[0].tenantID)
	}
	if subs[0].tenantName != "" {
		t.Errorf("subscription.tenantName = %q; want empty — it would label the customer's rows with disco's organization name", subs[0].tenantName)
	}

	// The Entities gate is NOT asserted here, deliberately. Watching for its
	// error message would be VACUOUS: the fake token this credential mints is
	// rejected by ARM with a 401, skipIfAccessDenied swallows that, and no
	// error is reported — so the assertion would pass with the gate deleted.
	// TestStitchTopHierarchy_SkipsTheEntitiesCallWithoutTenantScope covers it
	// properly, by refusing the token before any request is built.
	//
	// Note this test is NOT hermetic, and cannot cheaply be: the credential
	// SUCCEEDS (it has to, or nothing could be stamped), and scanSubscription
	// runs scanResourceGroups and loadRegisteredProviders before the
	// --services filter is consulted, so two requests to management.azure.com
	// are attempted whatever the filter says. They fail fast without egress
	// and 401 with it; either way the assertions above are unaffected. Do not
	// read the "no outbound call" property into this test — that belongs to
	// its sibling, which refuses the token instead.
}

// TestStitchTopHierarchy_SkipsTheEntitiesCallWithoutTenantScope isolates the
// gate's call site inside stitchTopHierarchy — the one the sibling tests
// cannot reach, because they run with no subscriptions and stitchTopHierarchy
// returns before the gate. For the live set of sites:
// grep -n 'tenantScopeEnabled()' internal/providers/azure/*.go
//
// The management-group Entities list is tenant-wide: it enumerates the
// CREDENTIAL's management-group tree, which under Lighthouse is disco's rather
// than the scanned customer's, and — like every tenant-scope call here — it
// SUCCEEDS. The other two
// hierarchy tiers are derived from rows this scan already wrote, so they must
// still link with the call suppressed; that is what separates this gate from
// simply not stitching.
func TestStitchTopHierarchy_SkipsTheEntitiesCallWithoutTenantScope(t *testing.T) {
	const subID = "11111111-1111-1111-1111-111111111111"
	st := newTestStore(t)

	subName, rgName := "Customer Prod", "rg-app"
	if _, err := st.UpsertResources([]*store.Resource{
		{
			Provider: "azure", AccountID: subID, Type: TypeSubscription,
			NativeID: "/subscriptions/" + subID, Name: &subName, DiscoveredBy: testScanID,
		},
		{
			Provider: "azure", AccountID: subID, Type: TypeResourcesResourceGroup,
			NativeID: "/subscriptions/" + subID + "/resourceGroups/rg-app",
			Name:     &rgName, DiscoveredBy: testScanID,
		},
	}); err != nil {
		t.Fatalf("seed resources: %v", err)
	}

	subs := []subscription{{ID: subID}}
	stitchTopHierarchy(t.Context(), subs, refusingCredential{t: t}, st, federatedCfg())

	// The gate must skip the Entities CALL, not the stitching. Without this
	// assertion the test passes just as well if suppression were implemented
	// by returning early — which would drop a tier the store could still link.
	edges, err := st.RelationshipsFrom(store.ResourceID("azure", subID, "/subscriptions/"+subID))
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Kind == "contains" {
			found = true
		}
	}
	if !found {
		t.Errorf("no contains edge from the subscription; the store-derived resource-group tier must still link with the Entities call gated (edges: %+v)", edges)
	}
}
