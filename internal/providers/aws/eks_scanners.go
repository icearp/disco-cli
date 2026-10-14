package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEKSCluster, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSAccessEntry, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSAddon, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSCapability, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSFargateProfile, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSIdentityProviderConfig, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSNodegroup, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSPodIdentityAssociation, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSAssociatedAccessPolicy, Service: "eks"})
	registerType(restype.Descriptor{Type: TypeEKSCertificateAuthority, Service: "eks"})
	registerService(serviceEntry{
		name: "aws:eks",
		fn:   scanEKS,
	})
}

// eksAPI is the narrow set of EKS operations called by scanEKSClusters.
// *eks.Client satisfies; tests substitute a stub.
type eksAPI interface {
	ListClusters(context.Context, *eks.ListClustersInput, ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	DescribeCluster(context.Context, *eks.DescribeClusterInput, ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}

// scanEKS discovers EKS clusters in one region. ListClusters returns names
// only; clusters are described in parallel to avoid N+1 sequential API calls.
//
// Phases run in order and stop at the first hard error. The associated access
// policies and certificate authorities run after every pre-existing phase, so
// their failure cannot cost the older types; the certificate-authority API is
// the newest, so it runs last.
func scanEKS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := eks.NewFromConfig(acct.cfg, func(o *eks.Options) { o.Region = region })
	var (
		clusters []childParent
		entries  []eksAccessEntryRef
	)
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			t, i, clusters, err = scanEKSClusters(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			t, i, entries, err = scanEKSExtended(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanEKSAnywhereSubscriptions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanEKSAssociatedAccessPolicies(ctx, client, acct, region, st, scanID, entries)
		},
		func() (int, int, error) {
			return scanEKSCertificateAuthorities(ctx, client, acct, region, st, scanID, clusters)
		},
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanEKSClusters holds the testable scan body. It also returns the clusters it
// stored, so a cluster whose describe was denied gets no child phase.
func scanEKSClusters(ctx context.Context, client eksAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var (
		mu     sync.Mutex
		stored []childParent
	)
	p := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	total, inserted, err := pageScanConcurrent(ctx, "eks:ListClusters", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*eks.ListClustersOutput, error) { return p.NextPage(c) },
		func(o *eks.ListClustersOutput) []string { return o.Clusters },
		func(gctx context.Context, name string) (*store.Resource, error) {
			desc, err := client.DescribeCluster(gctx, &eks.DescribeClusterInput{Name: &name})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("eks:DescribeCluster %s: %w", name, err)
			}
			c := desc.Cluster
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEKSCluster,
				NativeID:       sv(c.Arn),
				Name:           c.Name,
				Region:         &region,
				CreatedAt:      tp(c.CreatedAt),
				Status:         sp(string(c.Status)),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}
			if len(c.Tags) > 0 {
				s := mustJSON(c.Tags)
				r.TagsJSON = &s
			}
			mu.Lock()
			stored = append(stored, childParent{id: name, arn: r.NativeID})
			mu.Unlock()
			return r, nil
		}, 0)
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, stored, nil
}
