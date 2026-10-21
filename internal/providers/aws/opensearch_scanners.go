package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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
}

// scanOpenSearch discovers OpenSearch (and legacy Elasticsearch, same SDK)
// domains in one region, then the applications, data sources and account-level
// resources around them. ListDomainNames returns name-only entries;
// DescribeDomain carries the edge-bearing body. Per-item AccessDenied
// tolerated.
//
// Every phase calls the same endpoint, so a domain phase that cannot reach it
// (isEndpointUnreachable) is returned alone: the later phases would
// fail the same way. Any other domain-phase error, an API-coded throttle
// included, is joined like a later phase's, and the later phases still run;
// the data-source phase fans out over whatever domain names were listed.
// The phases are independent: a failed phase does not stop the ones after it,
// and their errors are joined. A transient network
// error from a later phase warns instead of being joined, because one
// transient member makes the dispatcher classify the whole joined error as
// transient, which reports every hard error beside it as a transient one.
// Attachments fan out over the applications listed earlier in the same scan.
func scanOpenSearch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := opensearch.NewFromConfig(acct.cfg, func(o *opensearch.Options) { o.Region = region })
	domainNames, total, inserted, derr := scanOpenSearchDomains(ctx, client, acct, region, st, scanID)
	if isEndpointUnreachable(derr) {
		return total, inserted, derr
	}
	errs := []error{derr}
	var apps []childParent
	for _, phase := range []struct {
		name string
		run  func() (int, int, error)
	}{
		{"opensearch:ListApplications", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			apps, t, i, err = scanOpenSearchApplications(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"opensearch:ListDataSources", func() (int, int, error) {
			return scanOpenSearchDataSources(ctx, client, acct, region, st, scanID, domainNames)
		}},
		{"opensearch:ListDirectQueryDataSources", func() (int, int, error) {
			return scanOpenSearchDirectQueryDataSources(ctx, client, acct, region, st, scanID)
		}},
		{"opensearch:ListDataSourceAttachments", func() (int, int, error) {
			return scanOpenSearchDataSourceAttachments(ctx, client, acct, region, st, scanID, apps)
		}},
		{"opensearch:ListVpcEndpoints", func() (int, int, error) {
			return scanOpenSearchVpcEndpoints(ctx, client, acct, region, st, scanID)
		}},
		{"opensearch:DescribePackages", func() (int, int, error) {
			return scanOpenSearchPackages(ctx, client, acct, region, st, scanID)
		}},
		{"opensearch:DescribeReservedInstances", func() (int, int, error) {
			return scanOpenSearchReservedInstances(ctx, client, acct, region, st, scanID)
		}},
		{"opensearch:DescribeOutboundConnections", func() (int, int, error) {
			return scanOpenSearchOutboundConnections(ctx, client, acct, region, st, scanID)
		}},
		{"opensearch:DescribeInboundConnections", func() (int, int, error) {
			return scanOpenSearchInboundConnections(ctx, client, acct, region, st, scanID)
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		if isDemotableTransient(perr) {
			_ = skipIfTransient(st, phase.name, acct.ID, region, perr)
			continue
		}
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

// openSearchARN builds an es-namespace ARN-shaped NativeID,
// arn:aws:es:{region}:{account}:{resource}. Used for the domain ARN a
// connection names by parts, and for resources the API returns no ARN for
// (VPC endpoints, packages, reserved instances), whose shape is synthetic.
func openSearchARN(region, accountID, resource string) string {
	return "arn:aws:es:" + region + ":" + accountID + ":" + resource
}

// scanOpenSearchDomains lists the region's domain names, describes and stores
// each domain, and returns the listed names (also when a describe fails) for
// the data-source phase.
func scanOpenSearchDomains(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (names []string, total, inserted int, err error) {
	listOut, lerr := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if lerr != nil {
		if isAccessDenied(lerr) {
			_ = skipIfAccessDenied(st, "opensearch:ListDomainNames", acct.ID, region, lerr)
			return nil, 0, 0, nil
		}
		return nil, 0, 0, fmt.Errorf("opensearch:ListDomainNames: %w", lerr)
	}
	for _, n := range listOut.DomainNames {
		if name := sv(n.DomainName); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		// Acquire fails once a describe has failed and cancelled gctx; stop
		// launching and let g.Wait return that first error, not Canceled.
		if sem.Acquire(gctx, 1) != nil {
			break
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeDomain(gctx, &opensearch.DescribeDomainInput{DomainName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("opensearch:DescribeDomain %s: %w", name, derr)
			}
			if out.DomainStatus == nil {
				return nil
			}
			arn := sv(out.DomainStatus.ARN)
			if arn == "" {
				return nil
			}
			created := false
			if out.DomainStatus.Created != nil {
				created = *out.DomainStatus.Created
			}
			status := "creating"
			if created {
				status = "active"
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeOpenSearchDomain,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.DomainStatus),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	werr := g.Wait()
	if werr == nil {
		// The launch loop also stops when the caller's ctx is cancelled, with
		// no describe having failed; that must not read as a complete listing.
		werr = ctx.Err()
	}
	if werr != nil {
		return names, 0, 0, werr
	}
	if len(batch) == 0 {
		return names, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return names, 0, 0, fmt.Errorf("upsert opensearch domains: %w", uerr)
	}
	return names, len(batch), n, nil
}
