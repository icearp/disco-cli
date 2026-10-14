package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/icearp/disco-cli/store"
)

// redshiftSatelliteAPI lists the ops of scanRedshift's account and cluster
// satellite phases.
type redshiftSatelliteAPI interface {
	DescribeAuthenticationProfiles(context.Context, *redshift.DescribeAuthenticationProfilesInput, ...func(*redshift.Options)) (*redshift.DescribeAuthenticationProfilesOutput, error)
	DescribeReservedNodes(context.Context, *redshift.DescribeReservedNodesInput, ...func(*redshift.Options)) (*redshift.DescribeReservedNodesOutput, error)
	DescribeQev2IdcApplications(context.Context, *redshift.DescribeQev2IdcApplicationsInput, ...func(*redshift.Options)) (*redshift.DescribeQev2IdcApplicationsOutput, error)
	DescribeCustomDomainAssociations(context.Context, *redshift.DescribeCustomDomainAssociationsInput, ...func(*redshift.Options)) (*redshift.DescribeCustomDomainAssociationsOutput, error)
	DescribePartners(context.Context, *redshift.DescribePartnersInput, ...func(*redshift.Options)) (*redshift.DescribePartnersOutput, error)
}

// redshiftListCodes are the modeled codes a region-wide list tolerates. skip
// ends the phase silently: the op's not-found code (the list is unfiltered, so
// it can only mean none exist) and UnsupportedOperation (a sub-feature gap
// inside a present service). warn ends it after one warning: modeled codes
// warn until confirmed benign.
type redshiftListCodes struct{ skip, warn []string }

// redshiftPageErr classifies a failed page of a region-wide list. It returns
// nil when the phase should stop and keep the rows it already collected: for a
// skip code silently, for a denial or a warn code after one warning. Anything
// else comes back wrapped with op.
func redshiftPageErr(st *store.Store, acct *account, region, op string, err error, codes redshiftListCodes) error {
	switch {
	case isAPIErrorCode(err, codes.skip...):
		return nil
	case isAccessDenied(err) || isAPIErrorCode(err, codes.warn...):
		return skipIfAccessDenied(st, op, acct.ID, region, err)
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

func scanRedshiftAuthenticationProfiles(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "redshift:DescribeAuthenticationProfiles"
	out, err := client.DescribeAuthenticationProfiles(ctx, &redshift.DescribeAuthenticationProfilesInput{})
	if err != nil {
		return 0, 0, redshiftPageErr(st, acct, region, op, err, redshiftListCodes{skip: []string{"AuthenticationProfileNotFoundFault"}})
	}
	var batch []*store.Resource
	for _, p := range out.AuthenticationProfiles {
		name := sv(p.AuthenticationProfileName)
		if name == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeRedshiftAuthenticationProfile, NativeID: redshiftARN(region, acct.ID, "authenticationprofile", name),
			Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "redshift authentication profiles")
}

func scanRedshiftReservedNodes(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "redshift:DescribeReservedNodes"
	pager := redshift.NewDescribeReservedNodesPaginator(client, &redshift.DescribeReservedNodesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if ferr := redshiftPageErr(st, acct, region, op, err, redshiftListCodes{
				skip: []string{"ReservedNodeNotFound"},
				warn: []string{"DependentServiceUnavailableFault"},
			}); ferr != nil {
				return 0, 0, ferr
			}
			break
		}
		for _, n := range out.ReservedNodes {
			id := sv(n.ReservedNodeId)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftReservedNode, NativeID: redshiftARN(region, acct.ID, "reservednode", id),
				Name: &id, Region: &region, Status: nonEmptyPtr(sv(n.State)), CreatedAt: tp(n.StartTime),
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift reserved nodes")
}

func scanRedshiftQev2IdcApplications(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "redshift:DescribeQev2IdcApplications"
	pager := redshift.NewDescribeQev2IdcApplicationsPaginator(client, &redshift.DescribeQev2IdcApplicationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if ferr := redshiftPageErr(st, acct, region, op, err, redshiftListCodes{
				skip: []string{"Qev2IdcApplicationNotExists", "UnsupportedOperation"},
				warn: []string{"DependentServiceAccessDenied", "DependentServiceUnavailableFault"},
			}); ferr != nil {
				return 0, 0, ferr
			}
			break
		}
		for _, a := range out.Qev2IdcApplications {
			arn := sv(a.Qev2IdcApplicationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftQev2IdcApplication, NativeID: arn,
				Name: a.Qev2IdcApplicationName, Region: &region, Status: nonEmptyPtr(sv(a.IdcOnboardStatus)),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift qev2 idc applications")
}

// scanRedshiftCustomDomainAssociations lists the region's custom domain
// associations (the API has no cluster filter). One Association groups every
// cluster/domain pair a certificate serves; each pair is its own association,
// keyed {clusterARN}/custom-domain-association/{domain} under a cluster this
// scan stored. Its row holds the Association narrowed to that pair, so a
// change to a sibling pair cannot version-split it. A pair whose cluster was
// not stored cannot be keyed and is skipped; with no stored clusters the call
// is not made.
func scanRedshiftCustomDomainAssociations(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent) (int, int, error) {
	const op = "redshift:DescribeCustomDomainAssociations"
	if len(clusters) == 0 {
		return 0, 0, nil
	}
	clusterARN := parentARNByName(clusters)
	var children []containedChild
	pager := redshift.NewDescribeCustomDomainAssociationsPaginator(client, &redshift.DescribeCustomDomainAssociationsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if ferr := redshiftPageErr(st, acct, region, op, err, redshiftListCodes{
				skip: []string{"CustomDomainAssociationNotFoundFault", "UnsupportedOperation"},
			}); ferr != nil {
				return 0, 0, ferr
			}
			break
		}
		for _, a := range out.Associations {
			for _, pair := range a.CertificateAssociations {
				domain := sv(pair.CustomDomainName)
				parent, ok := clusterARN[sv(pair.ClusterIdentifier)]
				if domain == "" || !ok {
					continue
				}
				one := a
				one.CertificateAssociations = []redshifttypes.CertificateAssociation{pair}
				children = append(children, containedChild{parentID: store.ResourceID("aws", acct.ID, parent), row: &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRedshiftCustomDomainAssociation, NativeID: parent + "/custom-domain-association/" + domain,
					Name: &domain, Region: &region,
					AttributesJSON: mustJSON(one), DiscoveredBy: scanID,
				}})
			}
		}
	}
	return upsertContained(st, op, children)
}

// scanRedshiftPartners lists each stored cluster's partner integrations, keyed
// {clusterARN}/partner/{database}/{partner}: the API identifies one by account,
// cluster, database and partner name. A cluster deleted since it was listed is
// skipped, and so is UnsupportedOperation (a sub-feature gap inside a present
// service). UnauthorizedPartnerIntegration warns once and drops that cluster:
// modeled codes warn until confirmed benign.
func scanRedshiftPartners(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent) (int, int, error) {
	const op = "redshift:DescribePartners"
	var gapOnce sync.Once
	isSkip := func(err error) bool {
		if isAPIErrorCode(err, "ClusterNotFound", "UnsupportedOperation") {
			return true
		}
		if isAPIErrorCode(err, "UnauthorizedPartnerIntegration") {
			gapOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
			return true
		}
		return false
	}
	accountID := acct.ID
	return childFanOut(ctx, st, acct, region, op, clusters, isSkip, func(ctx context.Context, c childParent) ([]*store.Resource, error) {
		out, err := client.DescribePartners(ctx, &redshift.DescribePartnersInput{AccountId: &accountID, ClusterIdentifier: &c.id})
		if err != nil {
			return nil, err
		}
		var rows []*store.Resource
		for _, p := range out.PartnerIntegrationInfoList {
			db, partner := sv(p.DatabaseName), sv(p.PartnerName)
			if db == "" || partner == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftPartner, NativeID: c.arn + "/partner/" + db + "/" + partner,
				Name: &partner, Region: &region, Status: nonEmptyPtr(string(p.Status)), CreatedAt: tp(p.CreatedAt),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		return rows, nil
	})
}
