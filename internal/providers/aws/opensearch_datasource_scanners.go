package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/icearp/disco-cli/store"
)

// scanOpenSearchDataSources discovers each domain's S3 direct-query data
// sources (legacy es:datasource). ListDataSources requires a DomainName, so
// it fans out over the names the domain phase listed (none when that listing
// failed); DataSourceDetails carries no ARN, so NativeID is synthesized as
// `{domainARN}/data-source/{name}`.
func scanOpenSearchDataSources(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string, domainNames []string) (total, inserted int, err error) {
	var batch []*store.Resource
	for _, name := range domainNames {
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
