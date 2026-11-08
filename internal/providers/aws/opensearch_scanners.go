package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOpenSearchDomain, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchApplication, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchDataSource, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchDirectQueryDataSource, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchDataSourceAttachment, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchVpcEndpoint, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchPackage, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchReservedInstance, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchOutboundConnection, Service: "opensearchservice"})
	registerType(restype.Descriptor{Type: TypeOpenSearchInboundConnection, Service: "opensearchservice"})
	registerService(serviceEntry{
		name: "aws:opensearch",
		fn:   scanOpenSearch,
	})
}

// opensearchAPI is the narrow set of OpenSearch operations the scan calls.
type opensearchAPI interface {
	ListDomainNames(context.Context, *opensearch.ListDomainNamesInput, ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error)
	DescribeDomain(context.Context, *opensearch.DescribeDomainInput, ...func(*opensearch.Options)) (*opensearch.DescribeDomainOutput, error)
	ListApplications(context.Context, *opensearch.ListApplicationsInput, ...func(*opensearch.Options)) (*opensearch.ListApplicationsOutput, error)
	ListDataSources(context.Context, *opensearch.ListDataSourcesInput, ...func(*opensearch.Options)) (*opensearch.ListDataSourcesOutput, error)
	ListDataSourceAttachments(context.Context, *opensearch.ListDataSourceAttachmentsInput, ...func(*opensearch.Options)) (*opensearch.ListDataSourceAttachmentsOutput, error)
	ListDirectQueryDataSources(context.Context, *opensearch.ListDirectQueryDataSourcesInput, ...func(*opensearch.Options)) (*opensearch.ListDirectQueryDataSourcesOutput, error)
	ListVpcEndpoints(context.Context, *opensearch.ListVpcEndpointsInput, ...func(*opensearch.Options)) (*opensearch.ListVpcEndpointsOutput, error)
	DescribeVpcEndpoints(context.Context, *opensearch.DescribeVpcEndpointsInput, ...func(*opensearch.Options)) (*opensearch.DescribeVpcEndpointsOutput, error)
	DescribePackages(context.Context, *opensearch.DescribePackagesInput, ...func(*opensearch.Options)) (*opensearch.DescribePackagesOutput, error)
	DescribeReservedInstances(context.Context, *opensearch.DescribeReservedInstancesInput, ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstancesOutput, error)
	DescribeOutboundConnections(context.Context, *opensearch.DescribeOutboundConnectionsInput, ...func(*opensearch.Options)) (*opensearch.DescribeOutboundConnectionsOutput, error)
	DescribeInboundConnections(context.Context, *opensearch.DescribeInboundConnectionsInput, ...func(*opensearch.Options)) (*opensearch.DescribeInboundConnectionsOutput, error)
	ListTags(context.Context, *opensearch.ListTagsInput, ...func(*opensearch.Options)) (*opensearch.ListTagsOutput, error)
}

const (
	osListDomainNamesOp  = "opensearch:ListDomainNames"
	osDescribeDomainOp   = "opensearch:DescribeDomain"
	osListApplicationsOp = "opensearch:ListApplications"
	osListTagsOp         = "opensearch:ListTags"
)

// scanOpenSearch discovers OpenSearch (and legacy Elasticsearch, same SDK)
// domains in one region, then the applications, data sources and account-level
// resources around them. Domains and applications are stored with their tags
// (openSearchTags), whose denials are one warning for the whole scan.
//
// The phases run one at a time, in order: the data-source phase fans out over
// the domain names listed, the attachment phase over the applications stored.
// A phase's error does not stop the later ones; a failed write and ctx's end
// do. Every phase calls the same endpoint, so a domain listing that cannot
// reach it (isEndpointUnreachable) is returned alone: the later phases would
// fail the same way. Once that listing has reached the endpoint, a later
// NXDOMAIN is a plain error (lateNXDOMAIN). The errors are joined by
// phaseErrs, so a lone or all-transient failure marks the scan partial.
func scanOpenSearch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := opensearch.NewFromConfig(acct.cfg, func(o *opensearch.Options) { o.Region = region })
	tags := openSearchTags(client).sharingDenials()
	var (
		domainNames []string
		apps        []childParent
	)
	phases := []hostPhase{
		{op: osListDomainNamesOp, run: func(p *phaseTotals) (failed bool) {
			domainNames, failed = scanOpenSearchDomains(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: osListApplicationsOp, run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanOpenSearchApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "opensearch:ListDataSources", func() (int, error) {
			return scanOpenSearchDataSources(ctx, client, acct, region, st, scanID, domainNames)
		}),
		countedPhase(0, "opensearch:ListDirectQueryDataSources", func() (int, error) {
			return scanOpenSearchDirectQueryDataSources(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "opensearch:ListDataSourceAttachments", func() (int, error) {
			return scanOpenSearchDataSourceAttachments(ctx, client, acct, region, st, scanID, apps)
		}),
		countedPhase(0, "opensearch:ListVpcEndpoints", func() (int, error) {
			return scanOpenSearchVpcEndpoints(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "opensearch:DescribePackages", func() (int, error) {
			return scanOpenSearchPackages(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "opensearch:DescribeReservedInstances", func() (int, error) {
			return scanOpenSearchReservedInstances(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "opensearch:DescribeOutboundConnections", func() (int, error) {
			return scanOpenSearchOutboundConnections(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "opensearch:DescribeInboundConnections", func() (int, error) {
			return scanOpenSearchInboundConnections(ctx, client, acct, region, st, scanID)
		}),
	}
	var p phaseTotals
	notRun := 0
	for n, ph := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - n
			break
		}
		before := len(p.errs)
		if ph.run(&p) {
			break
		}
		if osListingUnreachable(p.errs[before:]) {
			break
		}
		for i := before; i < len(p.errs); i++ {
			p.errs[i].err = lateNXDOMAIN(p.errs[i].op, p.errs[i].err)
		}
	}
	p.foldStop(ctx, "opensearch", notRun)
	tags.warnDenials(st, acct, region)
	return p.result(st, acct, region)
}

// osListingUnreachable reports whether errs holds the domain listing's error
// and it says the endpoint could not be reached. Only the first phase files
// under that op; a describe's or tag read's error does not count, as the
// listing reached the endpoint.
func osListingUnreachable(errs phaseErrs) bool {
	for _, f := range errs {
		if f.op == osListDomainNamesOp && isEndpointUnreachable(f.err) {
			return true
		}
	}
	return false
}

// openSearchTags reads a row's tags with ListTags, sent the row's NativeID:
// the ARN DescribeDomain or ListApplications returned. The Service Reference
// lists domain, application and datasource on ListTags and AddTags, each with
// aws:ResourceTag as a condition key; CreateDomain and CreateApplication take
// a TagList, and neither DomainStatus nor ApplicationSummary carries one. Its
// datasource is the direct-query data source, whose listing carries its
// TagList, so none is read for it. The op answers with every tag at once (no
// page token). ResourceNotFoundException, its modeled not-found, drops a
// resource deleted after it was listed.
func openSearchTags(client opensearchAPI) rowTagReader {
	return rowTagReader{
		op:       osListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTags(ctx, &opensearch.ListTagsInput{ARN: &arn})
			if err != nil {
				return nil, err
			}
			return openSearchTagsJSON(out.TagList), nil
		},
	}
}

// openSearchARN builds an es-namespace ARN-shaped NativeID,
// arn:{p}:es:{region}:{account}:{resource}. Used for the domain ARN a
// connection names by parts, and for resources the API returns no ARN for
// (VPC endpoints, packages, reserved instances), whose shape is synthetic.
func openSearchARN(partition, region, accountID, resource string) string {
	return arnFor(partition, "es", region, accountID, resource)
}

// osDomainRead is one domain's DescribeDomain result.
type osDomainRead struct {
	status *opensearchtypes.DomainStatus
	err    error
}

// scanOpenSearchDomains lists the region's domain names, describes each one
// and stores the domains with their tags (tags). It returns every name
// listed, also when its describe fails, for the data-source phase, and
// whether the write failed, after which the scan must stop.
//
// ListDomainNames returns names only, so a domain whose describe fails has no
// element carrying its ARN to store, and is dropped. One DescribeDomain no
// longer finds (ResourceNotFoundException, deleted after it was listed) is
// dropped silently. A denied describe warns once, after the write succeeded;
// any other error, ctx's end included, is returned after the other domains
// are stored, a transient one apart so that it is demoted beside a hard
// error. An NXDOMAIN is a plain error (lateNXDOMAIN): the listing resolved.
// scanOpenSearch folds ctx's end into one error.
func scanOpenSearchDomains(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (names []string, storeWriteFailed bool) {
	var listSkips, describeSkips notStoredSkips
	out, listErr := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if listErr == nil {
		for _, n := range out.DomainNames {
			if name := sv(n.DomainName); name != "" {
				names = append(names, name)
			} else {
				listSkips.skipped("domains have no name", noIdentifier)
			}
		}
	}
	reads, stopped := readEachKey(ctx, names, func(ctx context.Context, name string) osDomainRead {
		out, err := client.DescribeDomain(ctx, &opensearch.DescribeDomainInput{DomainName: &name})
		if err != nil {
			return osDomainRead{err: err}
		}
		return osDomainRead{status: out.DomainStatus}
	})
	var (
		rows       []*store.Resource
		denied     skipTally
		transients transientTally
		errs       []error
	)
	for i, read := range reads {
		name := names[i]
		switch err := lateNXDOMAIN(osDescribeDomainOp, read.err); {
		case err == nil:
			if read.status == nil || sv(read.status.ARN) == "" {
				describeSkips.skipped("domains were described without an ARN", name)
				continue
			}
			rows = append(rows, openSearchDomainRow(acct, region, scanID, name, read.status))
		case isAPIErrorCode(err, "ResourceNotFoundException"):
		case isAccessDenied(err):
			denied.skipped(name, err)
		case transients.drop(name, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", osDescribeDomainOp, name, err))
		}
	}
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", osDescribeDomainOp, stopped))
	}
	// Added before the write: these domains were never going to be stored,
	// so a transient one demoted to a warning beside a failed write still
	// says what happened to them.
	p.errs.add(osDescribeDomainOp, errors.Join(errs...))
	if t := &transients.skipTally; t.count > 0 {
		p.errs.add(osDescribeDomainOp, fmt.Errorf("%s: %d domain(s) not described, so not stored, first %s: %w", osDescribeDomainOp, t.count, t.first, t.err))
	}
	return names, storeRowsTags(ctx, st, acct, region, osListDomainNamesOp, rows, listErr, func() {
		listSkips.warn(st, osListDomainNamesOp, acct, region)
		describeSkips.warn(st, osDescribeDomainOp, acct, region)
		denied.warnDeniedIn(st, osDescribeDomainOp, acct, region, "domain(s) not stored as their describe was")
	}, tags, p)
}

// openSearchDomainRow is the row of the domain name, described as d, stored
// under d's ARN. Its Status is "active" once d says the domain is created,
// "creating" before.
func openSearchDomainRow(acct *account, region, scanID, name string, d *opensearchtypes.DomainStatus) *store.Resource {
	status := "creating"
	if d.Created != nil && *d.Created {
		status = "active"
	}
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeOpenSearchDomain,
		NativeID:       sv(d.ARN),
		Name:           &name,
		Region:         &region,
		Status:         &status,
		AttributesJSON: mustJSON(d),
		DiscoveredBy:   scanID,
	}
}
