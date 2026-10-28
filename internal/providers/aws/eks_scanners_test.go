package aws

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/icearp/disco-cli/store"
)

// Each cluster is stored from its DescribeCluster body: its ARN, name,
// status, creation time and tags, the body verbatim as its attributes.
func TestScanEKSClusters_StoresDescribedClusters(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	if _, _, err := eksxScan(context.Background(), st, calls.handlers()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeEKSCluster)
	if got := nativeIDs(rows); !slices.Equal(got, []string{eksClusterC1, "arn:aws:eks:us-east-1:123456789012:cluster/c2"}) {
		t.Fatalf("cluster rows = %v", got)
	}
	r := rows[eksClusterC1]
	if sv(r.Name) != "c1" || sv(r.Region) != testRegion || sv(r.Status) != "ACTIVE" || sv(r.CreatedAt) != "2026-08-20T10:00:00Z" {
		t.Errorf("c1 Name, Region, Status, CreatedAt = %v, %v, %v, %v", r.Name, r.Region, r.Status, r.CreatedAt)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"cluster": "c1"})
	if want := mustJSON(calls.cluster("c1")); r.AttributesJSON != want {
		t.Errorf("c1 attributes = %s; want %s", r.AttributesJSON, want)
	}
}

// A cluster whose describe fails is not stored. Deleted since it was listed,
// it is dropped silently with its per-cluster resources unlisted. Otherwise
// its per-cluster resources are still stored, but not its certificate
// authorities, which need a stored parent: a denial warns once under
// eks:DescribeCluster, a describe without a cluster or without an ARN warns
// once, a throttle is a transient error and any other code a hard one. The other cluster is stored
// in every case.
func TestScanEKSClusters_DescribeFailures(t *testing.T) {
	for _, c := range []struct {
		name         string
		describe     func() (any, error)
		outcome      serviceOutcome
		warns        int
		childrenRead bool
	}{
		{"denied", func() (any, error) { return nil, eksDenied }, 0, 1, true},
		{"not found", func() (any, error) { return nil, eksGone }, 0, 0, false},
		{"other error", func() (any, error) { return nil, eksBadInput }, outcomeError, 0, true},
		{"transient", func() (any, error) { return nil, eksxThrottled }, outcomeTransient, 0, true},
		{"no body", func() (any, error) { return &eks.DescribeClusterOutput{}, nil }, 0, 1, true},
		{"no ARN", func() (any, error) {
			return &eks.DescribeClusterOutput{Cluster: &ekstypes.Cluster{Name: ptrStr("c1")}}, nil
		}, 0, 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "eks:DescribeCluster")
			calls := newEKSXCalls()
			h := calls.handlers()
			describe := h["DescribeCluster"]
			h["DescribeCluster"] = func(in any) (any, error) {
				if sv(in.(*eks.DescribeClusterInput).Name) == "c1" {
					return c.describe()
				}
				return describe(in)
			}
			var caCalls parentCalls
			h["ListCertificateAuthorities"] = func(in any) (any, error) {
				caCalls.add(sv(in.(*eks.ListCertificateAuthoritiesInput).ClusterName))
				return &eks.ListCertificateAuthoritiesOutput{}, nil
			}

			total, _, err := eksxScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if len(*warnings) != c.warns {
				t.Errorf("eks:DescribeCluster warnings = %v; want %d", *warnings, c.warns)
			}
			rows := rowsByNativeID(t, st, TypeEKSCluster)
			if _, ok := rows[eksClusterC1]; ok || len(rows) != 1 {
				t.Errorf("cluster rows = %v; want c2 alone", nativeIDs(rows))
			}
			if _, ok := eksxRowOf(t, st, TypeEKSAddon, calls.childID("addon", "c1", "addon-c1-t")); ok != c.childrenRead {
				t.Errorf("c1's add-on stored = %v; want %v", ok, c.childrenRead)
			}
			want := 15 // c2 and its 14 resources
			if c.childrenRead {
				want += 14
			}
			if total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if got := caCalls.distinct(); !slices.Equal(got, []string{"c2"}) {
				t.Errorf("certificate authorities listed for %v; want [c2]", got)
			}
		})
	}
}

// A throttled describe beside a hard failure, of another listing or of the
// other cluster's describe, is demoted to one warning under
// eks:DescribeCluster.
func TestScanEKSClusters_DescribeThrottleDemotedBesideHardError(t *testing.T) {
	for _, c := range []struct {
		name    string
		listing string // fails hard when set
		c2Fails bool   // c2's describe fails hard
	}{
		{"another listing fails", "ListAddons", false},
		{"the other describe fails", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "eks:DescribeCluster")
			calls := newEKSXCalls()
			h := calls.handlers()
			describe := h["DescribeCluster"]
			h["DescribeCluster"] = func(in any) (any, error) {
				switch sv(in.(*eks.DescribeClusterInput).Name) {
				case "c1":
					return nil, eksxThrottled
				case "c2":
					if c.c2Fails {
						return nil, eksBadInput
					}
				}
				return describe(in)
			}
			if c.listing != "" {
				h[c.listing] = func(any) (any, error) { return nil, eksBadInput }
			}

			_, _, err := eksxScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidParameterException")
			if *warns != 1 {
				t.Errorf("eks:DescribeCluster warnings = %d; want 1", *warns)
			}
		})
	}
}

// A ListClusters page that fails keeps the clusters of the page before it,
// with their per-cluster resources, and returns the error.
func TestScanEKSClusters_ListFailureKeepsPage(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	h := calls.handlers()
	h["ListClusters"] = failOnCall(2, eksBadInput, h["ListClusters"])

	total, _, err := eksxScan(context.Background(), st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeEKSCluster)); !slices.Equal(got, []string{eksClusterC1}) {
		t.Errorf("cluster rows = %v; want [%s]", got, eksClusterC1)
	}
	if total != 15 {
		t.Errorf("total = %d; want 15, c1 and its 14 resources", total)
	}
}

// A denied ListClusters warns once under its op; no per-cluster listing runs,
// and the later account-wide listing still does.
func TestScanEKSClusters_DeniedListing(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	opWarns := countOpWarnings(st, "eks:ListClusters")
	calls := newEKSXCalls()
	h := calls.handlers()
	h["ListClusters"] = func(any) (any, error) { return nil, eksDenied }

	total, _, err := eksxScan(context.Background(), st, h)
	if err != nil || total != 0 {
		t.Fatalf("scan = %d, %v; want 0, nil", total, err)
	}
	if *warns != 1 || *opWarns != 1 {
		t.Errorf("warnings = %d (eks:ListClusters %d); want 1 under the op", *warns, *opWarns)
	}
	if calls.count("ListAddons") != 0 || calls.count("ListEksAnywhereSubscriptions") != 1 {
		t.Errorf("ListAddons, ListEksAnywhereSubscriptions calls = %d, %d; want 0, 1", calls.count("ListAddons"), calls.count("ListEksAnywhereSubscriptions"))
	}
}

// A failed cluster write stops the scan with no warning about the clusters
// it lost and no per-cluster listing, and still returns the describe errors
// met before it: a hard one and a transient one.
func TestScanEKSClusters_FailedWriteStops(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeEKSCluster)
	calls := newEKSXCalls()
	h := calls.handlers()
	h["ListClusters"] = calls.answer("ListClusters", func(any) any {
		return &eks.ListClustersOutput{Clusters: []string{"c1", "c2", "c3", "c4"}}
	})
	describe := h["DescribeCluster"]
	h["DescribeCluster"] = func(in any) (any, error) {
		switch sv(in.(*eks.DescribeClusterInput).Name) {
		case "c1":
			return nil, eksDenied
		case "c3":
			return nil, eksBadInput
		case "c4":
			return nil, eksxThrottled
		}
		return describe(in)
	}

	_, _, err := eksxScan(context.Background(), st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want a store write failure", err)
	}
	assertGlueOutcome(t, err, outcomeStoreWrite)
	assertAPICode(t, err, "InvalidParameterException")
	if !errors.Is(err, eksxThrottled) {
		t.Errorf("err = %v; want it to carry the throttled describe", err)
	}
	if *warns != 0 || calls.count("ListAccessEntries") != 0 {
		t.Errorf("warnings, ListAccessEntries calls = %d, %d; want 0, 0", *warns, calls.count("ListAccessEntries"))
	}
}
