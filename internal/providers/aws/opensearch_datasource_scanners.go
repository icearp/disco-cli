package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/icearp/disco-cli/store"
)

// scanOpenSearchDataSources discovers each domain's S3 direct-query data
// sources (legacy es:datasource). ListDataSources requires a DomainName, so
// it fans out per domain; DataSourceDetails carries no ARN, so NativeID is
// synthesized as `{domainARN}/data-source/{name}`.
func scanOpenSearchDataSources(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	listOut, lerr := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if lerr != nil {
		if isAccessDenied(lerr) {
			_ = skipIfAccessDenied(st, "opensearch:ListDomainNames", acct.ID, region, lerr)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("opensearch:ListDomainNames: %w", lerr)
	}
	var batch []*store.Resource
	for _, n := range listOut.DomainNames {
		name := sv(n.DomainName)
		if name == "" {
			continue
		}
		out, derr := client.ListDataSources(ctx, &opensearch.ListDataSourcesInput{DomainName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "opensearch:ListDataSources", acct.ID, region, derr)
				continue
			}
			return 0, 0, fmt.Errorf("opensearch:ListDataSources %s: %w", name, derr)
		}
		domainARN := fmt.Sprintf("arn:aws:es:%s:%s:domain/%s", region, acct.ID, name)
		for _, ds := range out.DataSources {
			dsName := sv(ds.Name)
			if dsName == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchDataSource, NativeID: domainARN + "/data-source/" + dsName,
				Name: ds.Name, Region: &region, Status: sp(string(ds.Status)),
				AttributesJSON: mustJSON(ds), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "opensearch data-sources")
}
