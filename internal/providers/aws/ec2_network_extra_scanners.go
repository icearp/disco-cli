package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2SecurityGroupRule, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2SubnetCidrReservation, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointConnection, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointAssociation, Service: "ec2"})
}

// scanEC2NetworkExtra discovers VPC-networking children that the core
// networking scanner does not list: security group rules, subnet CIDR
// reservations, and VPC endpoint connections and associations.
func scanEC2NetworkExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanSecurityGroupRules(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSubnetCidrReservations(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEndpointConnections(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEndpointAssociations(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanSecurityGroupRules(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSecurityGroupRules", acct, region, st,
		ec2.NewDescribeSecurityGroupRulesPaginator(client, &ec2.DescribeSecurityGroupRulesInput{}),
		func(page *ec2.DescribeSecurityGroupRulesOutput) []*store.Resource {
			var out []*store.Resource
			for _, r := range page.SecurityGroupRules {
				arn := sv(r.SecurityGroupRuleArn)
				if arn == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecurityGroupRule,
					NativeID:       arn,
					Name:           ec2TagName(r.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(r.Tags),
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanSubnetCidrReservations fans out GetSubnetCidrReservations per subnet:
// SubnetId is required and EC2 has no account-wide reservation listing, so the
// N+1 call per owned subnet is the only way to enumerate them. The subnets are
// listed here rather than read from the store because the networking scanner
// that stores them runs concurrently.
func scanSubnetCidrReservations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	subnetIDs, err := listSubnetIDs(ctx, client, acct)
	if err != nil || len(subnetIDs) == 0 {
		return 0, 0, err
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, subnetID := range subnetIDs {
		g.Go(func() error {
			reservations, err := getSubnetCidrReservations(gctx, client, subnetID)
			if err != nil {
				return err
			}
			rows := make([]*store.Resource, 0, len(reservations))
			for _, r := range reservations {
				id := sv(r.SubnetCidrReservationId)
				if id == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SubnetCidrReservation,
					NativeID:       ec2ARN(region, acct.ID, "subnet-cidr-reservation", id),
					Name:           ec2TagName(r.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(r.Tags),
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			batch = append(batch, rows...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		// A denial is IAM-wide for the op, not per subnet: warn once instead of
		// once per subnet.
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ec2:GetSubnetCidrReservations", acct.ID, region, err)
		}
		if isEC2RegionFeatureGap(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return upsertBatch(st, batch, "ec2 subnet-cidr-reservations")
}

// getSubnetCidrReservations returns the IPv4 and IPv6 reservations of one
// subnet. GetSubnetCidrReservations has no SDK paginator. A subnet deleted
// since it was listed yields no reservations.
func getSubnetCidrReservations(ctx context.Context, client ec2API, subnetID string) ([]ec2types.SubnetCidrReservation, error) {
	var out []ec2types.SubnetCidrReservation
	var token *string
	for {
		page, err := client.GetSubnetCidrReservations(ctx, &ec2.GetSubnetCidrReservationsInput{
			SubnetId:  &subnetID,
			NextToken: token,
		})
		if err != nil {
			if isAPIErrorCode(err, "InvalidSubnetID.NotFound") {
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:GetSubnetCidrReservations %s: %w", subnetID, err)
		}
		out = append(out, page.SubnetIpv4CidrReservations...)
		out = append(out, page.SubnetIpv6CidrReservations...)
		if sv(page.NextToken) == "" {
			return out, nil
		}
		token = page.NextToken
	}
}

// listSubnetIDs returns the ids of the subnets this account owns. Subnets
// shared in through RAM are dropped: their reservations belong to the owner
// account, which scans them itself, and querying them here would be denied or
// store them under the wrong account.
func listSubnetIDs(ctx context.Context, client ec2API, acct *account) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// The networking phase calls DescribeSubnets in the same scope and
			// already warns on this denial; a second warning adds nothing.
			if isAccessDenied(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeSubnets (list IDs): %w", err)
		}
		for _, s := range page.Subnets {
			if id := sv(s.SubnetId); id != "" && sv(s.OwnerId) == acct.ID {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// scanVPCEndpointConnections lists the consumer endpoints connected to
// endpoint services this account owns. The consumer endpoint may live in
// another account; the connection itself is a resource of the service owner
// (AWS documents its ARN as vpc-endpoint-connection/<VpcEndpointConnectionId>
// under the owner's account).
func scanVPCEndpointConnections(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeVpcEndpointConnections", acct, region, st,
		ec2.NewDescribeVpcEndpointConnectionsPaginator(client, &ec2.DescribeVpcEndpointConnectionsInput{}),
		func(page *ec2.DescribeVpcEndpointConnectionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, c := range page.VpcEndpointConnections {
				id := sv(c.VpcEndpointConnectionId)
				if id == "" {
					continue
				}
				status := string(c.VpcEndpointState)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VPCEndpointConnection,
					NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint-connection", id),
					Name:           ec2TagName(c.Tags),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(c.Tags),
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanVPCEndpointAssociations — DescribeVpcEndpointAssociations has no SDK
// paginator. AWS documents no ARN for an association, so the NativeID is
// synthesized in ec2ARN shape from its Id.
func scanVPCEndpointAssociations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var token *string
	for {
		page, err := client.DescribeVpcEndpointAssociations(ctx, &ec2.DescribeVpcEndpointAssociationsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ec2:DescribeVpcEndpointAssociations", acct.ID, region, err)
			}
			if isEC2RegionFeatureGap(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ec2:DescribeVpcEndpointAssociations: %w", err)
		}
		for _, a := range page.VpcEndpointAssociations {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCEndpointAssociation,
				NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint-association", id),
				Name:           ec2TagName(a.Tags),
				Region:         &region,
				Status:         a.AssociatedResourceAccessibility,
				TagsJSON:       awsTagsJSON(a.Tags),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		if sv(page.NextToken) == "" {
			break
		}
		token = page.NextToken
	}
	return upsertBatch(st, batch, "ec2 vpc-endpoint-associations")
}
