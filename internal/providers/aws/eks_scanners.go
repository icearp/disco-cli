package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
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

// eksPageCap is the model's maxResults cap (1 to 100) of every EKS listing
// that sends one.
const eksPageCap = 100

// scanEKS discovers the EKS resources of one region. Every listing runs as
// its own phase, one at a time (runHostPhases): a phase's error does not stop
// the later ones; a failed write and ctx's end do. The per-cluster listings
// read the clusters ListClusters returned, those clusters concurrently, and
// read each row's tags. The associated access policies read the access
// entries stored this scan and the certificate authorities the clusters
// stored, since each of their rows is recorded under that parent. The
// certificate-authority API is the newest, so it runs last.
func scanEKS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := eks.NewFromConfig(acct.cfg, func(o *eks.Options) { o.Region = region })
	var (
		listed, stored []childParent
		entries        []eksAccessEntryRef
	)
	return runHostPhases(ctx, st, acct, region, "eks", 0, []hostPhase{
		{op: "eks:ListClusters", run: func(p *phaseTotals) (failed bool) {
			listed, stored, failed = scanEKSClusters(ctx, client, acct, region, st, scanID, p)
			return failed
		}},
		{op: "eks:ListAccessEntries", run: func(p *phaseTotals) (failed bool) {
			entries, failed = scanEKSAccessEntries(ctx, client, acct, region, st, scanID, listed, p)
			return failed
		}},
		{op: "eks:ListAddons", run: func(p *phaseTotals) bool {
			return scanEKSAddons(ctx, client, acct, region, st, scanID, listed, p)
		}},
		{op: "eks:ListCapabilities", run: func(p *phaseTotals) bool {
			return scanEKSCapabilities(ctx, client, acct, region, st, scanID, listed, p)
		}},
		{op: "eks:ListFargateProfiles", run: func(p *phaseTotals) bool {
			return scanEKSFargateProfiles(ctx, client, acct, region, st, scanID, listed, p)
		}},
		{op: "eks:ListIdentityProviderConfigs", run: func(p *phaseTotals) bool {
			return scanEKSIdentityProviderConfigs(ctx, client, acct, region, st, scanID, listed, p)
		}},
		{op: "eks:ListNodegroups", run: func(p *phaseTotals) bool {
			return scanEKSNodegroups(ctx, client, acct, region, st, scanID, listed, p)
		}},
		{op: "eks:ListPodIdentityAssociations", run: func(p *phaseTotals) bool {
			return scanEKSPodIdentityAssociations(ctx, client, acct, region, st, scanID, listed, p)
		}},
		countedPhase(0, "eks:ListEksAnywhereSubscriptions", func() (int, int, error) {
			return scanEKSAnywhereSubscriptions(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "eks:ListAssociatedAccessPolicies", func() (int, int, error) {
			return scanEKSAssociatedAccessPolicies(ctx, client, acct, region, st, scanID, entries)
		}),
		countedPhase(0, "eks:ListCertificateAuthorities", func() (int, int, error) {
			return scanEKSCertificateAuthorities(ctx, client, acct, region, st, scanID, stored)
		}),
	})
}

// eksClusterRead is one cluster's DescribeCluster result.
type eksClusterRead struct {
	cluster *ekstypes.Cluster
	err     error
}

// scanEKSClusters lists the cluster names, describes each one and stores the
// clusters with the tags DescribeCluster returns. It returns the clusters
// listed, for the per-cluster listings, and the clusters stored, for the
// certificate authorities, whose rows are recorded under a stored cluster.
//
// ListClusters returns names only, so a cluster whose describe fails has no
// element to store. One DescribeCluster no longer finds
// (ResourceNotFoundException, deleted after it was listed) is dropped
// silently, and its per-cluster resources are not listed. Any other failure
// still has them listed, as their listings do not need the cluster's row: a
// denied describe warns once, after the write succeeded; any other error is
// returned after the others are stored, a transient one apart so that it is
// demoted beside a hard error. It reports whether the write failed, after
// which the scanner must stop.
func scanEKSClusters(ctx context.Context, client eksAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (listed, stored []childParent, storeWriteFailed bool) {
	const (
		listOp     = "eks:ListClusters"
		describeOp = "eks:DescribeCluster"
	)
	names, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
		out, err := client.ListClusters(ctx, &eks.ListClustersInput{NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
		if err != nil {
			return nil, nil, err
		}
		return out.Clusters, out.NextToken, nil
	})
	reads, stopped := readEachKey(ctx, names, func(ctx context.Context, name string) eksClusterRead {
		out, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: &name})
		if err != nil {
			return eksClusterRead{err: err}
		}
		return eksClusterRead{cluster: out.Cluster}
	})
	var (
		rows       []*store.Resource
		skips      notStoredSkips
		denied     skipTally
		transients transientTally
		errs       []error
	)
	for i, read := range reads {
		name := names[i]
		switch err := read.err; {
		case err == nil:
			c := read.cluster
			if c == nil || sv(c.Arn) == "" {
				skips.skipped("clusters were described without an ARN", name)
				break
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSCluster, NativeID: sv(c.Arn), Name: c.Name, Region: &region,
				CreatedAt: tp(c.CreatedAt), Status: sp(string(c.Status)),
				TagsJSON: mapTagsJSON(c.Tags), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
			stored = append(stored, childParent{id: name, arn: sv(c.Arn)})
		case isEKSNotFound(err):
			continue
		case isAccessDenied(err):
			denied.skipped(name, err)
		case transients.drop(name, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", describeOp, name, err))
		}
		listed = append(listed, childParent{id: name})
	}
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", describeOp, stopped))
	}
	var transient error
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: %d cluster(s) not described, so not stored, first %s: %w", describeOp, t.count, t.first, t.err)
	}
	hard := errors.Join(errs...)
	total, inserted, err := storeReadRows(st, acct, region, listOp, rows, listErr, func() {
		skips.warn(st, describeOp, acct, region)
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, describeOp, acct.ID, region, fmt.Errorf("%d cluster(s) not stored, first %s: %w", denied.count, denied.first, denied.err))
		}
	})
	if errors.Is(err, store.ErrStoreWrite) {
		p.addPhase(listOp, total, inserted, errors.Join(err, hard, transient))
		return nil, nil, true
	}
	p.errs.add(describeOp, hard)
	p.errs.add(describeOp, transient)
	p.addPhase(listOp, total, inserted, err)
	return listed, stored, false
}
