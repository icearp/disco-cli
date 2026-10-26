package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/icearp/disco-cli/store"
)

// openSearchPageLimit is the documented MaxResults cap of the paginated
// Describe* ops below.
const openSearchPageLimit = 100

// openSearchDirectQueryDataSourceAttrs shadows the DataSourceType union, which
// marshals as an unlabelled {"Value": ...}, with one key per known member so
// the stored row names the source kind. A member newer than the pinned SDK is
// kept under UnknownMember.
type openSearchDirectQueryDataSourceAttrs struct {
	opensearchtypes.DirectQueryDataSource
	DataSourceType openSearchDirectQueryDataSourceTypeAttrs
}

type openSearchDirectQueryDataSourceTypeAttrs struct {
	CloudWatchLog *opensearchtypes.CloudWatchDirectQueryDataSource   `json:",omitempty"`
	Prometheus    *opensearchtypes.PrometheusDirectQueryDataSource   `json:",omitempty"`
	SecurityLake  *opensearchtypes.SecurityLakeDirectQueryDataSource `json:",omitempty"`
	UnknownMember *opensearchtypes.UnknownUnionMember                `json:",omitempty"`
}

func flattenOpenSearchDirectQueryDataSource(d opensearchtypes.DirectQueryDataSource) openSearchDirectQueryDataSourceAttrs {
	out := openSearchDirectQueryDataSourceAttrs{DirectQueryDataSource: d}
	switch m := d.DataSourceType.(type) {
	case *opensearchtypes.DirectQueryDataSourceTypeMemberCloudWatchLog:
		out.DataSourceType.CloudWatchLog = &m.Value
	case *opensearchtypes.DirectQueryDataSourceTypeMemberPrometheus:
		out.DataSourceType.Prometheus = &m.Value
	case *opensearchtypes.DirectQueryDataSourceTypeMemberSecurityLake:
		out.DataSourceType.SecurityLake = &m.Value
	case *opensearchtypes.UnknownUnionMember:
		out.DataSourceType.UnknownMember = m
	}
	return out
}

// scanOpenSearchDirectQueryDataSources discovers the account-level direct
// query data sources (CloudWatch Logs, Security Lake, Prometheus). They are
// distinct from the per-domain S3 data sources scanOpenSearchDataSources
// lists, and carry their own ARN. The list carries every field
// GetDirectQueryDataSource returns, so no per-item Get is made. A region whose
// endpoint does not serve the op is skipped silently; rows listed before a
// later failure are stored and counted.
func scanOpenSearchDirectQueryDataSources(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "opensearch:ListDirectQueryDataSources"
	var batch []*store.Resource
	in := &opensearch.ListDirectQueryDataSourcesInput{}
	for {
		out, err := client.ListDirectQueryDataSources(ctx, in)
		if err != nil {
			switch {
			case in.NextToken == nil && isOpNotDeployed(err):
				err = nil
			case isAccessDenied(err):
				err = skipIfAccessDenied(st, op, acct.ID, region, err)
			default:
				err = fmt.Errorf("%s: %w", op, err)
			}
			total, inserted, uerr := upsertBatch(st, batch, op)
			return total, inserted, errors.Join(err, uerr)
		}
		for _, d := range out.DirectQueryDataSources {
			arn := sv(d.DataSourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchDirectQueryDataSource, NativeID: arn,
				Name: d.DataSourceName, Region: &region,
				TagsJSON:       openSearchTagsJSON(d.TagList),
				AttributesJSON: mustJSON(flattenOpenSearchDirectQueryDataSource(d)), DiscoveredBy: scanID,
			})
		}
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return upsertListed(st, batch, op, err)
		}
		in.NextToken = out.NextToken
	}
}

func openSearchTagsJSON(tags []opensearchtypes.Tag) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[sv(t.Key)] = sv(t.Value)
	}
	return mapTagsJSON(m)
}

// scanOpenSearchVpcEndpoints discovers the OpenSearch-managed (PrivateLink)
// VPC endpoints this account created, possibly to another account's domain.
// ListVpcEndpoints carries no subnets or security groups, so each list page's
// ids are described in one DescribeVpcEndpoints call. The batch reuses the
// list's page size because the model sets no limit on VpcEndpointIdList. An
// endpoint has no ARN, so its NativeID is the synthetic
// arn:{p}:es:{region}:{account}:vpc-endpoint/{VpcEndpointId}.
func scanOpenSearchVpcEndpoints(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const listOp = "opensearch:ListVpcEndpoints"
	var (
		pages   [][]string
		pageErr error
	)
	in := &opensearch.ListVpcEndpointsInput{}
	for {
		out, err := client.ListVpcEndpoints(ctx, in)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, listOp, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", listOp, err)
		}
		var ids []string
		for _, e := range out.VpcEndpointSummaryList {
			if id := sv(e.VpcEndpointId); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			pages = append(pages, ids)
		}
		var done bool
		if done, pageErr = lastPage(in.NextToken, out.NextToken); done {
			break
		}
		in.NextToken = out.NextToken
	}
	batch, failed, err := describeOpenSearchVpcEndpoints(ctx, client, acct, region, st, scanID, pages)
	if len(failed) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: openSearchDescribeVpcEndpointsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d endpoints not described, so not stored: %s", len(failed), strings.Join(failed, ", ")),
		})
	}
	if pageErr != nil {
		pageErr = fmt.Errorf("%s: %w", listOp, pageErr)
	}
	total, inserted, uerr := upsertBatch(st, batch, "opensearch vpc-endpoints")
	return total, inserted, errors.Join(pageErr, err, uerr)
}

const openSearchDescribeVpcEndpointsOp = "opensearch:DescribeVpcEndpoints"

// describeOpenSearchVpcEndpoints describes each page of endpoint ids and
// returns the rows built so far even when a later page fails. An endpoint
// deleted since it was listed comes back as ENDPOINT_NOT_FOUND and is dropped
// silently; every other per-endpoint error is returned in failed as
// "{id} ({code}: {message})" for the caller to report once.
func describeOpenSearchVpcEndpoints(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string, pages [][]string) (batch []*store.Resource, failed []string, err error) {
	for _, ids := range pages {
		out, derr := client.DescribeVpcEndpoints(ctx, &opensearch.DescribeVpcEndpointsInput{VpcEndpointIds: ids})
		if derr != nil {
			if isAccessDenied(derr) {
				return batch, failed, skipIfAccessDenied(st, openSearchDescribeVpcEndpointsOp, acct.ID, region, derr)
			}
			return batch, failed, fmt.Errorf("%s: %w", openSearchDescribeVpcEndpointsOp, derr)
		}
		for _, e := range out.VpcEndpointErrors {
			if e.ErrorCode != opensearchtypes.VpcEndpointErrorCodeEndpointNotFound {
				failed = append(failed, fmt.Sprintf("%s (%s: %s)", sv(e.VpcEndpointId), e.ErrorCode, sv(e.ErrorMessage)))
			}
		}
		for _, e := range out.VpcEndpoints {
			id := sv(e.VpcEndpointId)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchVpcEndpoint, NativeID: openSearchARN(acct.partition(), region, acct.ID, "vpc-endpoint/"+id),
				Name: e.VpcEndpointId, Region: &region, Status: nonEmptyPtr(string(e.Status)),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return batch, failed, nil
}

// openSearchAWSPackageOwner is the PackageOwner the AWS optional-plugins guide
// gives for the plugins AWS publishes to every account. The model leaves
// PackageOwner an unconstrained string, so this value is the documented one,
// not a modelled enum. Those plugins are a catalog, not this account's
// resources, so they are not stored.
const openSearchAWSPackageOwner = "AWS"

// scanOpenSearchPackages discovers custom packages (dictionaries, plugins,
// licenses, configs). A package has no ARN, so its NativeID is the synthetic
// arn:{p}:es:{region}:{account}:package/{PackageID}. DELETED packages are
// skipped. Domain associations are not fetched: ListDomainsForPackage is one
// call per package.
func scanOpenSearchPackages(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := opensearch.NewDescribePackagesPaginator(client, &opensearch.DescribePackagesInput{},
		func(o *opensearch.DescribePackagesPaginatorOptions) { o.Limit = openSearchPageLimit })
	return pageScan(ctx, "opensearch:DescribePackages", acct, region, st,
		p.HasMorePages,
		func(ctx context.Context) (*opensearch.DescribePackagesOutput, error) { return p.NextPage(ctx) },
		func(out *opensearch.DescribePackagesOutput) []opensearchtypes.PackageDetails {
			return out.PackageDetailsList
		},
		func(pkg opensearchtypes.PackageDetails) *store.Resource {
			id := sv(pkg.PackageID)
			if id == "" || sv(pkg.PackageOwner) == openSearchAWSPackageOwner || pkg.PackageStatus == opensearchtypes.PackageStatusDeleted {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchPackage, NativeID: openSearchARN(acct.partition(), region, acct.ID, "package/"+id),
				Name: pkg.PackageName, Region: &region, Status: nonEmptyPtr(string(pkg.PackageStatus)),
				CreatedAt: tp(pkg.CreatedAt), AttributesJSON: mustJSON(pkg), DiscoveredBy: scanID,
			}
		})
}

// scanOpenSearchReservedInstances discovers purchased reserved-instance
// reservations, in every state, as the EC2/RDS/ElastiCache reservation
// scanners do. A reservation has no ARN, so its NativeID is the synthetic
// arn:{p}:es:{region}:{account}:reserved-instance/{ReservedInstanceId}.
func scanOpenSearchReservedInstances(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := opensearch.NewDescribeReservedInstancesPaginator(client, &opensearch.DescribeReservedInstancesInput{},
		func(o *opensearch.DescribeReservedInstancesPaginatorOptions) { o.Limit = openSearchPageLimit })
	return pageScan(ctx, "opensearch:DescribeReservedInstances", acct, region, st,
		p.HasMorePages,
		func(ctx context.Context) (*opensearch.DescribeReservedInstancesOutput, error) { return p.NextPage(ctx) },
		func(out *opensearch.DescribeReservedInstancesOutput) []opensearchtypes.ReservedInstance {
			return out.ReservedInstances
		},
		func(ri opensearchtypes.ReservedInstance) *store.Resource {
			id := sv(ri.ReservedInstanceId)
			if id == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchReservedInstance, NativeID: openSearchARN(acct.partition(), region, acct.ID, "reserved-instance/"+id),
				Name: ri.ReservationName, Region: &region, Status: nonEmptyPtr(sv(ri.State)),
				CreatedAt: tp(ri.StartTime), AttributesJSON: mustJSON(ri), DiscoveredBy: scanID,
			}
		})
}

// openSearchConnectionDomainARN is the ARN of the domain a connection side
// names, or "" when a part is missing.
func openSearchConnectionDomainARN(partition string, c *opensearchtypes.DomainInformationContainer) string {
	if c == nil || c.AWSDomainInformation == nil {
		return ""
	}
	d := c.AWSDomainInformation
	region, owner, name := sv(d.Region), sv(d.OwnerId), sv(d.DomainName)
	if region == "" || owner == "" || name == "" {
		return ""
	}
	return openSearchARN(partition, region, owner, "domain/"+name)
}

// Cross-cluster connections are stored once per side, under the side's own
// domain: an outbound connection under its local (source) domain, an inbound
// one under its remote (destination) domain, which is the domain in the
// scanned account. Both sides report the same ConnectionId, so the NativeIDs
// `{domainARN}/outbound-connection/{id}` and `{domainARN}/inbound-connection/{id}`
// keep the two rows apart when both domains are in one account; the elements
// differ too (only the outbound side has an alias and properties). DELETED
// connections are skipped.

// scanOpenSearchOutboundConnections discovers this region's outbound
// cross-cluster connections.
func scanOpenSearchOutboundConnections(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := opensearch.NewDescribeOutboundConnectionsPaginator(client, &opensearch.DescribeOutboundConnectionsInput{},
		func(o *opensearch.DescribeOutboundConnectionsPaginatorOptions) { o.Limit = openSearchPageLimit })
	return pageScan(ctx, "opensearch:DescribeOutboundConnections", acct, region, st,
		p.HasMorePages,
		func(ctx context.Context) (*opensearch.DescribeOutboundConnectionsOutput, error) {
			return p.NextPage(ctx)
		},
		func(out *opensearch.DescribeOutboundConnectionsOutput) []opensearchtypes.OutboundConnection {
			return out.Connections
		},
		func(c opensearchtypes.OutboundConnection) *store.Resource {
			id, domainARN := sv(c.ConnectionId), openSearchConnectionDomainARN(acct.partition(), c.LocalDomainInfo)
			var status string
			if c.ConnectionStatus != nil {
				status = string(c.ConnectionStatus.StatusCode)
			}
			if id == "" || domainARN == "" || status == string(opensearchtypes.OutboundConnectionStatusCodeDeleted) {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchOutboundConnection, NativeID: domainARN + "/outbound-connection/" + id,
				Name: c.ConnectionAlias, Region: &region, Status: nonEmptyPtr(status),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
		})
}

// scanOpenSearchInboundConnections discovers the cross-cluster connections
// other domains opened to this region's domains.
func scanOpenSearchInboundConnections(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := opensearch.NewDescribeInboundConnectionsPaginator(client, &opensearch.DescribeInboundConnectionsInput{},
		func(o *opensearch.DescribeInboundConnectionsPaginatorOptions) { o.Limit = openSearchPageLimit })
	return pageScan(ctx, "opensearch:DescribeInboundConnections", acct, region, st,
		p.HasMorePages,
		func(ctx context.Context) (*opensearch.DescribeInboundConnectionsOutput, error) {
			return p.NextPage(ctx)
		},
		func(out *opensearch.DescribeInboundConnectionsOutput) []opensearchtypes.InboundConnection {
			return out.Connections
		},
		func(c opensearchtypes.InboundConnection) *store.Resource {
			id, domainARN := sv(c.ConnectionId), openSearchConnectionDomainARN(acct.partition(), c.RemoteDomainInfo)
			var status string
			if c.ConnectionStatus != nil {
				status = string(c.ConnectionStatus.StatusCode)
			}
			if id == "" || domainARN == "" || status == string(opensearchtypes.InboundConnectionStatusCodeDeleted) {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchInboundConnection, NativeID: domainARN + "/inbound-connection/" + id,
				Name: c.ConnectionId, Region: &region, Status: nonEmptyPtr(status),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
		})
}
