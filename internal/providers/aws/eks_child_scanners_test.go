package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/icearp/disco-cli/store"
)

const (
	eksPrincipalA = "arn:aws:iam::123456789012:role/a"
	eksPrincipalB = "arn:aws:iam::123456789012:user/b"
	eksAdminARN   = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy"
	eksViewARN    = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"

	eksEntryC1A = "arn:aws:eks:us-east-1:123456789012:access-entry/c1/arn:aws:iam::123456789012:role/a"
	eksAdminC1A = "arn:aws:eks:us-east-1:123456789012:associated-access-policy/c1/arn:aws:iam::123456789012:role/a/arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy"
	eksViewC1A  = "arn:aws:eks:us-east-1:123456789012:associated-access-policy/c1/arn:aws:iam::123456789012:role/a/arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"
	eksAdminC2A = "arn:aws:eks:us-east-1:123456789012:associated-access-policy/c2/arn:aws:iam::123456789012:role/a/arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy"

	eksClusterC1 = "arn:aws:eks:us-east-1:123456789012:cluster/c1"
	eksCA1       = "arn:aws:eks:us-east-1:123456789012:certificate-authority/c1/ca-1"
	eksCA2       = "arn:aws:eks:us-east-1:123456789012:certificate-authority/c1/ca-2"
)

var eksCACreated = time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

// eksTopLevelHandlers answers clusters c1 and c2 (described, so their rows
// exist), one EKS Anywhere subscription (the last pre-existing phase) and:
//   - ListAccessEntries: c1 over two pages (A; B and an empty principal), c2 A.
//   - ListAssociatedAccessPolicies: c1/A over two pages (admin; a
//     namespace-scoped view and an element with no policy ARN), c2/A admin,
//     c1/B none.
//   - ListCertificateAuthorities: c1 over two pages (ca-1; ca-2, ca-gone whose
//     describe returns no CA, and an element with no id), c2 none.
//
// Each child list records "cluster" or "cluster|principal" for the parent it
// was called for. Every other op is denied and warn-skipped by its phase.
func eksTopLevelHandlers(policyCalls, caCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListClusters": func(any) (any, error) { return &eks.ListClustersOutput{Clusters: []string{"c1", "c2"}}, nil },
		"DescribeCluster": func(in any) (any, error) {
			name := sv(in.(*eks.DescribeClusterInput).Name)
			arn := "arn:aws:eks:us-east-1:123456789012:cluster/" + name
			return &eks.DescribeClusterOutput{Cluster: &ekstypes.Cluster{Arn: &arn, Name: &name}}, nil
		},
		"ListEksAnywhereSubscriptions": func(any) (any, error) {
			return &eks.ListEksAnywhereSubscriptionsOutput{Subscriptions: []ekstypes.EksAnywhereSubscription{
				{Arn: ptrStr("arn:aws:eks:us-east-1:123456789012:eks-anywhere-subscription/s-1")},
			}}, nil
		},
		"ListAccessEntries": func(in any) (any, error) {
			p := in.(*eks.ListAccessEntriesInput)
			switch {
			case sv(p.ClusterName) == "c2":
				return &eks.ListAccessEntriesOutput{AccessEntries: []string{eksPrincipalA}}, nil
			case p.NextToken == nil:
				return &eks.ListAccessEntriesOutput{AccessEntries: []string{eksPrincipalA}, NextToken: ptrStr("p2")}, nil
			default:
				return &eks.ListAccessEntriesOutput{AccessEntries: []string{eksPrincipalB, ""}}, nil
			}
		},
		"ListAssociatedAccessPolicies": func(in any) (any, error) {
			p := in.(*eks.ListAssociatedAccessPoliciesInput)
			cluster, principal := sv(p.ClusterName), sv(p.PrincipalArn)
			policyCalls.add(cluster + "|" + principal)
			admin := ekstypes.AssociatedAccessPolicy{
				PolicyArn: ptrStr(eksAdminARN), AssociatedAt: &eksCACreated,
				AccessScope: &ekstypes.AccessScope{Type: ekstypes.AccessScopeTypeCluster},
			}
			switch {
			case principal != eksPrincipalA:
				return &eks.ListAssociatedAccessPoliciesOutput{}, nil
			case cluster == "c2":
				return &eks.ListAssociatedAccessPoliciesOutput{AssociatedAccessPolicies: []ekstypes.AssociatedAccessPolicy{admin}}, nil
			case p.NextToken == nil:
				return &eks.ListAssociatedAccessPoliciesOutput{AssociatedAccessPolicies: []ekstypes.AssociatedAccessPolicy{admin}, NextToken: ptrStr("p2")}, nil
			default:
				return &eks.ListAssociatedAccessPoliciesOutput{AssociatedAccessPolicies: []ekstypes.AssociatedAccessPolicy{
					{PolicyArn: ptrStr(eksViewARN), AccessScope: &ekstypes.AccessScope{Type: ekstypes.AccessScopeTypeNamespace, Namespaces: []string{"team-a"}}},
					{AccessScope: &ekstypes.AccessScope{Type: ekstypes.AccessScopeTypeCluster}},
				}}, nil
			}
		},
		"ListCertificateAuthorities": func(in any) (any, error) {
			p := in.(*eks.ListCertificateAuthoritiesInput)
			cluster := sv(p.ClusterName)
			caCalls.add(cluster)
			switch {
			case cluster != "c1":
				return &eks.ListCertificateAuthoritiesOutput{}, nil
			case p.NextToken == nil:
				return &eks.ListCertificateAuthoritiesOutput{CertificateAuthorities: []ekstypes.CertificateAuthoritySummary{{Id: ptrStr("ca-1")}}, NextToken: ptrStr("p2")}, nil
			default:
				return &eks.ListCertificateAuthoritiesOutput{CertificateAuthorities: []ekstypes.CertificateAuthoritySummary{
					{Id: ptrStr("ca-2")}, {Id: ptrStr("ca-gone")}, {SigningStatus: ekstypes.CertificateAuthoritySigningStatusNotUsed},
				}}, nil
			}
		},
		"DescribeCertificateAuthority": func(in any) (any, error) {
			id := sv(in.(*eks.DescribeCertificateAuthorityInput).CertificateAuthorityId)
			if id == "ca-gone" {
				return &eks.DescribeCertificateAuthorityOutput{}, nil
			}
			ca := &ekstypes.CertificateAuthority{
				Id: &id, CreatedAt: &eksCACreated, Data: ptrStr("cert-" + id),
				Validity: &ekstypes.CertificateAuthorityValidity{NotAfter: &eksCACreated},
			}
			if id == "ca-1" {
				ca.SigningStatus = ekstypes.CertificateAuthoritySigningStatusInUse
			}
			return &eks.DescribeCertificateAuthorityOutput{CertificateAuthority: ca}, nil
		},
	}
}

func scanEKSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) error {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, eksDenied)
	_, _, err := scanEKS(context.Background(), acct, testRegion, st, testScanID)
	return err
}

var (
	eksDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: eks:List")
	eksBadInput = apiErr("InvalidParameterException", "bad")
	eksGone     = apiErr("ResourceNotFoundException", "not found")
)

func sortedStrings(xs ...string) []string {
	sort.Strings(xs)
	return xs
}

func TestScanEKS_StoresAssociatedAccessPoliciesUnderTheirEntry(t *testing.T) {
	st := newTestStore(t)
	var policyCalls, caCalls parentCalls
	if err := scanEKSTopLevel(t, st, eksTopLevelHandlers(&policyCalls, &caCalls)); err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	wantCalls := sortedStrings("c1|"+eksPrincipalA, "c1|"+eksPrincipalB, "c2|"+eksPrincipalA)
	if got := policyCalls.distinct(); !slices.Equal(got, wantCalls) {
		t.Errorf("policies listed for %v, want %v", got, wantCalls)
	}
	rows := rowsByNativeID(t, st, TypeEKSAssociatedAccessPolicy)
	if got, want := nativeIDs(rows), sortedStrings(eksAdminC1A, eksViewC1A, eksAdminC2A); !slices.Equal(got, want) {
		t.Fatalf("associated policy rows = %v, want %v", got, want)
	}
	assertContainsNative(t, st, eksEntryC1A, eksAdminC1A)
	assertContainsNative(t, st, eksEntryC1A, eksViewC1A)
	assertContainsNative(t, st, "arn:aws:eks:us-east-1:123456789012:access-entry/c2/arn:aws:iam::123456789012:role/a", eksAdminC2A)
	assertContainsEdgesDeclared(t, st)

	admin := rows[eksAdminC1A]
	if sv(admin.Name) != eksAdminARN || sv(admin.Region) != testRegion || sv(admin.CreatedAt) != "2026-08-20T10:00:00Z" {
		t.Errorf("admin name/region/created = %q/%q/%q", sv(admin.Name), sv(admin.Region), sv(admin.CreatedAt))
	}
	var view struct {
		AccessScope struct {
			Type       string
			Namespaces []string
		}
	}
	if err := json.Unmarshal([]byte(rows[eksViewC1A].AttributesJSON), &view); err != nil {
		t.Fatalf("view attrs: %v", err)
	}
	if view.AccessScope.Type != "namespace" || !slices.Equal(view.AccessScope.Namespaces, []string{"team-a"}) {
		t.Errorf("view AccessScope = %+v", view.AccessScope)
	}
}

// An IAM path may hold "/" and ":", so a principal can spell the tail of an
// association's id. Building that id as {accessEntryNativeID}/access-policy/
// {policyArn} would merge the association into this entry's identity.
func TestScanEKS_AssociationNeverSharesAnAccessEntryIdentity(t *testing.T) {
	st := newTestStore(t)
	var policyCalls, caCalls parentCalls
	h := eksTopLevelHandlers(&policyCalls, &caCalls)
	spoof := eksPrincipalA + "/access-policy/" + eksAdminARN
	h["ListAccessEntries"] = func(any) (any, error) {
		return &eks.ListAccessEntriesOutput{AccessEntries: []string{eksPrincipalA, spoof}}, nil
	}
	if err := scanEKSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	entries := rowsByNativeID(t, st, TypeEKSAccessEntry)
	spoofEntry := "arn:aws:eks:us-east-1:123456789012:access-entry/c1/arn:aws:iam::123456789012:role/a/access-policy/arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy"
	if _, ok := entries[spoofEntry]; !ok {
		t.Fatalf("access entry rows = %v, want %s among them", nativeIDs(entries), spoofEntry)
	}
	assoc, ok := rowsByNativeID(t, st, TypeEKSAssociatedAccessPolicy)[eksAdminC1A]
	if !ok || assoc.ID == entries[spoofEntry].ID {
		t.Errorf("association row found=%v id=%s, access entry id=%s; want two identities", ok, assoc.ID, entries[spoofEntry].ID)
	}
}

func TestScanEKS_StoresCertificateAuthoritiesUnderTheirCluster(t *testing.T) {
	st := newTestStore(t)
	var policyCalls, caCalls parentCalls
	if err := scanEKSTopLevel(t, st, eksTopLevelHandlers(&policyCalls, &caCalls)); err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	if got := caCalls.distinct(); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Errorf("CAs listed for %v, want [c1 c2]", got)
	}
	rows := rowsByNativeID(t, st, TypeEKSCertificateAuthority)
	if got := nativeIDs(rows); !slices.Equal(got, []string{eksCA1, eksCA2}) {
		t.Fatalf("CA rows = %v, want [%s %s]", got, eksCA1, eksCA2)
	}
	assertContainsNative(t, st, eksClusterC1, eksCA1)
	assertContainsNative(t, st, eksClusterC1, eksCA2)
	assertContainsEdgesDeclared(t, st)

	ca1, ca2 := rows[eksCA1], rows[eksCA2]
	if sv(ca1.Name) != "ca-1" || sv(ca1.Region) != testRegion || sv(ca1.Status) != "IN_USE" || sv(ca1.CreatedAt) != "2026-08-20T10:00:00Z" {
		t.Errorf("ca-1 name/region/status/created = %q/%q/%q/%q", sv(ca1.Name), sv(ca1.Region), sv(ca1.Status), sv(ca1.CreatedAt))
	}
	if ca2.Status != nil {
		t.Errorf("ca-2 status = %q, want unset for an empty signing status", sv(ca2.Status))
	}
	var attrs struct {
		Data     string
		Validity struct{ NotAfter time.Time }
	}
	if err := json.Unmarshal([]byte(ca1.AttributesJSON), &attrs); err != nil {
		t.Fatalf("ca-1 attrs: %v", err)
	}
	if attrs.Data != "cert-ca-1" || !attrs.Validity.NotAfter.Equal(eksCACreated) {
		t.Errorf("ca-1 attrs = %s", ca1.AttributesJSON)
	}
}

func TestScanEKS_NoParentsNoChildCalls(t *testing.T) {
	st := newTestStore(t)
	var policyCalls, caCalls parentCalls
	h := eksTopLevelHandlers(&policyCalls, &caCalls)
	h["ListClusters"] = func(any) (any, error) { return &eks.ListClustersOutput{}, nil }
	if err := scanEKSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	if len(policyCalls.distinct()) != 0 || len(caCalls.distinct()) != 0 {
		t.Errorf("policy calls = %v, CA calls = %v; want none", policyCalls.distinct(), caCalls.distinct())
	}
	if n := len(rowsByNativeID(t, st, TypeEKSAssociatedAccessPolicy)) + len(rowsByNativeID(t, st, TypeEKSCertificateAuthority)); n != 0 {
		t.Errorf("child rows = %d, want 0", n)
	}
}

// eksFailKeys wraps a child handler so the keys bad1 and bad2 fail with err.
// The key is "cluster|principal" for ListAssociatedAccessPolicies, the cluster
// for ListCertificateAuthorities and the CA id for DescribeCertificateAuthority.
func eksFailKeys(next func(in any) (any, error), err error, bad1, bad2 string) func(in any) (any, error) {
	return func(in any) (any, error) {
		var key string
		switch p := in.(type) {
		case *eks.ListAssociatedAccessPoliciesInput:
			key = sv(p.ClusterName) + "|" + sv(p.PrincipalArn)
		case *eks.ListCertificateAuthoritiesInput:
			key = sv(p.ClusterName)
		case *eks.DescribeCertificateAuthorityInput:
			key = sv(p.CertificateAuthorityId)
		}
		if key == bad1 || key == bad2 {
			return nil, err
		}
		return next(in)
	}
}

// A failed parent or CA drops only itself: deleted since listing is silent, a
// denial warns once however many keys hit it, and any other error fails the
// scan.
func TestScanEKS_FailedChildKey(t *testing.T) {
	for _, tc := range []struct {
		op, bad1, bad2, childType string
		want                      []string
	}{
		{
			op: "ListAssociatedAccessPolicies", bad1: "c1|" + eksPrincipalA, bad2: "c1|" + eksPrincipalB,
			childType: TypeEKSAssociatedAccessPolicy, want: []string{eksAdminC2A},
		},
		{
			op: "ListCertificateAuthorities", bad1: "c1", bad2: "c2",
			childType: TypeEKSCertificateAuthority, want: []string{},
		},
		{
			op: "DescribeCertificateAuthority", bad1: "ca-1", bad2: "ca-gone",
			childType: TypeEKSCertificateAuthority, want: []string{eksCA2},
		},
	} {
		for _, ec := range []struct {
			name     string
			err      error
			wantWarn int
		}{
			{"parent gone", eksGone, 0},
			{"denied", eksDenied, 1},
		} {
			t.Run(tc.op+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "eks:"+tc.op)
				var policyCalls, caCalls parentCalls
				h := eksTopLevelHandlers(&policyCalls, &caCalls)
				h[tc.op] = eksFailKeys(h[tc.op], ec.err, tc.bad1, tc.bad2)
				if err := scanEKSTopLevel(t, st, h); err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if *warns != ec.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, ec.wantWarn)
				}
				if got := nativeIDs(rowsByNativeID(t, st, tc.childType)); !slices.Equal(got, tc.want) {
					t.Errorf("rows = %v, want %v", got, tc.want)
				}
			})
		}
		t.Run(tc.op+"/other", func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls, caCalls parentCalls
			h := eksTopLevelHandlers(&policyCalls, &caCalls)
			h[tc.op] = eksFailKeys(h[tc.op], eksBadInput, tc.bad1, tc.bad1)
			assertAPICode(t, scanEKSTopLevel(t, st, h), "InvalidParameterException")
		})
	}
}

// Each new op runs after every pre-existing phase, and its hard error reaches
// the caller.
func TestScanEKS_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, op := range []string{"ListAssociatedAccessPolicies", "ListCertificateAuthorities", "DescribeCertificateAuthority"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls, caCalls parentCalls
			h := eksTopLevelHandlers(&policyCalls, &caCalls)
			var failed atomic.Bool
			h[op] = func(any) (any, error) {
				failed.Store(true)
				return nil, eksBadInput
			}
			err := scanEKSTopLevel(t, st, h)
			if !failed.Load() {
				t.Fatalf("%s never called", op)
			}
			assertAPICode(t, err, "InvalidParameterException")
			if n := len(rowsByNativeID(t, st, TypeEKSAnywhereSubscription)); n != 1 {
				t.Errorf("anywhere subscription rows = %d, want 1: %s ran before the last pre-existing phase", n, op)
			}
		})
	}
}

// The certificate-authority API is the newest, so its hard error leaves every
// associated access policy stored.
func TestScanEKS_CertificateAuthorityErrorKeepsAccessPolicies(t *testing.T) {
	for _, op := range []string{"ListCertificateAuthorities", "DescribeCertificateAuthority"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls, caCalls parentCalls
			h := eksTopLevelHandlers(&policyCalls, &caCalls)
			h[op] = func(any) (any, error) { return nil, eksBadInput }
			assertAPICode(t, scanEKSTopLevel(t, st, h), "InvalidParameterException")
			if n := len(rowsByNativeID(t, st, TypeEKSAssociatedAccessPolicy)); n != 3 {
				t.Errorf("associated policy rows = %d, want 3: %s ran before the access policies", n, op)
			}
		})
	}
}

// Every stored row counts once in scanEKS's totals: 2 clusters, 3 access
// entries, 1 EKS Anywhere subscription, 3 associated access policies and 2
// certificate authorities. On a fresh store every row is new.
func TestScanEKS_TotalsCountEveryPhase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var policyCalls, caCalls parentCalls
	acct.cfg = opStubConfig(eksTopLevelHandlers(&policyCalls, &caCalls), eksDenied)
	total, inserted, err := scanEKS(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	if total != 11 || inserted != 11 {
		t.Errorf("scanEKS totals = (%d, %d), want (11, 11)", total, inserted)
	}
}

// A cluster whose describe is denied has no row, so its certificate
// authorities are not listed and no contains pair points at a missing parent.
func TestScanEKS_DeniedClusterGetsNoCertificateAuthorities(t *testing.T) {
	st := newTestStore(t)
	hierarchyWarns := countOpWarnings(st, "hierarchy")
	var policyCalls, caCalls parentCalls
	h := eksTopLevelHandlers(&policyCalls, &caCalls)
	describe := h["DescribeCluster"]
	h["DescribeCluster"] = func(in any) (any, error) {
		if sv(in.(*eks.DescribeClusterInput).Name) == "c1" {
			return nil, eksDenied
		}
		return describe(in)
	}
	if err := scanEKSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	if got := caCalls.distinct(); !slices.Equal(got, []string{"c2"}) {
		t.Errorf("CAs listed for %v, want [c2]", got)
	}
	if n := len(rowsByNativeID(t, st, TypeEKSCertificateAuthority)); n != 0 {
		t.Errorf("CA rows = %d, want 0", n)
	}
	if *hierarchyWarns != 0 {
		t.Errorf("hierarchy warnings = %d, want 0", *hierarchyWarns)
	}
}

// The client is pinned to the scanned region, not the config's default.
func TestScanEKS_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var policyCalls, caCalls, regions parentCalls
	cfg := opStubConfig(eksTopLevelHandlers(&policyCalls, &caCalls), eksDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanEKS(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanEKS: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	ca := "arn:aws:eks:eu-west-1:123456789012:certificate-authority/c1/ca-1"
	if row, ok := rowsByNativeID(t, st, TypeEKSCertificateAuthority)[ca]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("CA row = %+v (found %v), want region eu-west-1", row, ok)
	}
}
