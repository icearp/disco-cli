package aws

import (
	"context"
	"errors"
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

// scanRedshiftAuthenticationProfiles reads the op's single, unpaginated page.
func scanRedshiftAuthenticationProfiles(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeAuthenticationProfiles",
		redshiftListCodes{skip: []string{"AuthenticationProfileNotFoundFault"}},
		func(ctx context.Context, _ *string) (*redshift.DescribeAuthenticationProfilesOutput, error) {
			return client.DescribeAuthenticationProfiles(ctx, &redshift.DescribeAuthenticationProfilesInput{})
		},
		func(o *redshift.DescribeAuthenticationProfilesOutput) ([]redshifttypes.AuthenticationProfile, *string) {
			return o.AuthenticationProfiles, nil
		},
		func(p redshifttypes.AuthenticationProfile) *store.Resource {
			name := sv(p.AuthenticationProfileName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftAuthenticationProfile, NativeID: redshiftIDARN(region, acct.ID, "authenticationprofile", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			}
		}, "authentication profiles", "AuthenticationProfileName")
	return total, inserted, err
}

func scanRedshiftReservedNodes(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	codes := redshiftListCodes{skip: []string{"ReservedNodeNotFound"}, warn: []string{"DependentServiceUnavailableFault"}}
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeReservedNodes", codes,
		func(ctx context.Context, marker *string) (*redshift.DescribeReservedNodesOutput, error) {
			return client.DescribeReservedNodes(ctx, &redshift.DescribeReservedNodesInput{Marker: marker})
		},
		func(o *redshift.DescribeReservedNodesOutput) ([]redshifttypes.ReservedNode, *string) {
			return o.ReservedNodes, o.Marker
		},
		func(n redshifttypes.ReservedNode) *store.Resource {
			id := sv(n.ReservedNodeId)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftReservedNode, NativeID: redshiftIDARN(region, acct.ID, "reservednode", id),
				Name: &id, Region: &region, Status: nonEmptyPtr(sv(n.State)), CreatedAt: tp(n.StartTime),
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			}
		}, "reserved nodes", "ReservedNodeId")
	return total, inserted, err
}

func scanRedshiftQev2IdcApplications(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	codes := redshiftListCodes{
		skip: []string{"Qev2IdcApplicationNotExists", "UnsupportedOperation"},
		warn: []string{"DependentServiceAccessDenied", "DependentServiceUnavailableFault"},
	}
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeQev2IdcApplications", codes,
		func(ctx context.Context, marker *string) (*redshift.DescribeQev2IdcApplicationsOutput, error) {
			return client.DescribeQev2IdcApplications(ctx, &redshift.DescribeQev2IdcApplicationsInput{Marker: marker})
		},
		func(o *redshift.DescribeQev2IdcApplicationsOutput) ([]redshifttypes.Qev2IdcApplication, *string) {
			return o.Qev2IdcApplications, o.Marker
		},
		func(a redshifttypes.Qev2IdcApplication) *store.Resource {
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftQev2IdcApplication, NativeID: sv(a.Qev2IdcApplicationArn),
				Name: a.Qev2IdcApplicationName, Region: &region, Status: nonEmptyPtr(sv(a.IdcOnboardStatus)),
				AttributesJSON: mustJSON(a), TagsJSON: kvTagsJSON(a.Tags, redshiftTagKV), DiscoveredBy: scanID,
			}
		}, "Query Editor v2 IdC applications", "Qev2IdcApplicationArn")
	return total, inserted, err
}

// scanRedshiftCustomDomainAssociations lists the region's custom domain
// associations (the API has no cluster filter). One Association groups every
// cluster/domain pair a certificate serves; each pair is its own association,
// keyed {clusterARN}/custom-domain-association/{domain} under a cluster this
// scan stored. Its row holds the Association narrowed to that pair, so a
// change to a sibling pair cannot version-split it. A pair whose cluster was
// not stored cannot be keyed and is skipped; a pair with no domain is warned
// about. With no stored clusters the call is not made. Pages fail as in
// redshiftList, keeping the pairs already read.
func scanRedshiftCustomDomainAssociations(ctx context.Context, client redshiftSatelliteAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent) (int, int, error) {
	const op = "redshift:DescribeCustomDomainAssociations"
	if len(clusters) == 0 {
		return 0, 0, nil
	}
	clusterARN := parentARNByName(clusters)
	codes := redshiftListCodes{skip: []string{"CustomDomainAssociationNotFoundFault", "UnsupportedOperation"}}
	var (
		children []containedChild
		noDomain skipTally
		marker   *string
		listErr  error
	)
	for {
		out, err := client.DescribeCustomDomainAssociations(ctx, &redshift.DescribeCustomDomainAssociationsInput{Marker: marker})
		if err != nil {
			listErr = redshiftListPageErr(st, acct, region, op, "custom domain associations", marker != nil, err, codes)
			break
		}
		for _, a := range out.Associations {
			for _, pair := range a.CertificateAssociations {
				domain, cluster := sv(pair.CustomDomainName), sv(pair.ClusterIdentifier)
				if domain == "" {
					noDomain.skipped(fmt.Sprintf("cluster %q certificate %q", cluster, sv(a.CustomDomainCertificateArn)), nil)
					continue
				}
				parent, ok := clusterARN[cluster]
				if !ok {
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
		last, terr := lastPage(marker, out.Marker)
		if last {
			if terr != nil {
				listErr = fmt.Errorf("%s: %w", op, terr)
			}
			break
		}
		marker = out.Marker
	}
	warnNotStored(st, op, acct, region, &noDomain, "custom domain associations have no CustomDomainName")
	total, inserted, uerr := upsertContained(st, op, children)
	return total, inserted, errors.Join(listErr, uerr)
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
