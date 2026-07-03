package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudsearch"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cloudsearch",
		fn:   scanCloudSearch,
		emits: []coverage.TypeDecl{
			{Service: "cloudsearch", DiscoType: TypeCloudSearchDomain, Leaf: true},
		},
	})
}

type cloudSearchAPI interface {
	ListDomainNames(context.Context, *cloudsearch.ListDomainNamesInput, ...func(*cloudsearch.Options)) (*cloudsearch.ListDomainNamesOutput, error)
	DescribeDomains(context.Context, *cloudsearch.DescribeDomainsInput, ...func(*cloudsearch.Options)) (*cloudsearch.DescribeDomainsOutput, error)
}

// scanCloudSearch discovers CloudSearch domains. ListDomainNames enumerates the
// region's domains, then DescribeDomains fetches each one's full status (the
// list returns only name→state). Neither op is paginated.
func scanCloudSearch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := cloudsearch.NewFromConfig(acct.cfg, func(o *cloudsearch.Options) { o.Region = region })
	return scanCloudSearchWithClient(ctx, client, acct, region, st, scanID)
}

func scanCloudSearchWithClient(ctx context.Context, client cloudSearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	names, err := client.ListDomainNames(ctx, &cloudsearch.ListDomainNamesInput{})
	if err != nil {
		// Accounts AWS hasn't made eligible for CloudSearch get NotAuthorized
		// "New domain creation not supported on this account" — not self-
		// enableable (requires AWS Support), so (account: not entitled).
		if isAPIErrorWithMessage(err, "NotAuthorized", "not supported on this account") {
			return 0, 0, markServiceNotEntitled(err)
		}
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "cloudsearch:ListDomainNames", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("cloudsearch:ListDomainNames: %w", err)
	}
	if len(names.DomainNames) == 0 {
		return 0, 0, nil
	}
	domainNames := make([]string, 0, len(names.DomainNames))
	for name := range names.DomainNames {
		domainNames = append(domainNames, name)
	}

	out, err := client.DescribeDomains(ctx, &cloudsearch.DescribeDomainsInput{DomainNames: domainNames})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "cloudsearch:DescribeDomains", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("cloudsearch:DescribeDomains: %w", err)
	}
	var batch []*store.Resource
	for _, d := range out.DomainStatusList {
		arn := sv(d.ARN)
		if arn == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCloudSearchDomain, NativeID: arn,
			Name: d.DomainName, Region: &region,
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cloudsearch domains")
}
