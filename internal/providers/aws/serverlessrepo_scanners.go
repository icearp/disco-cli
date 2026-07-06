package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/serverlessapplicationrepository"
)

func init() {
	registerService(serviceEntry{
		name: "aws:serverlessrepo",
		fn:   scanServerlessRepo,
		emits: []coverage.TypeDecl{
			{Service: "serverlessrepo", DiscoType: TypeServerlessRepoApplication, Leaf: true},
		},
	})
}

// serverlessRepoAPI is the narrow surface scanServerlessRepoApplications uses.
// ListApplications is paginator-native, returns owned applications; the
// summary's ApplicationId carries the application ARN.
type serverlessRepoAPI interface {
	ListApplications(context.Context, *serverlessapplicationrepository.ListApplicationsInput, ...func(*serverlessapplicationrepository.Options)) (*serverlessapplicationrepository.ListApplicationsOutput, error)
}

func scanServerlessRepo(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := serverlessapplicationrepository.NewFromConfig(acct.cfg, func(o *serverlessapplicationrepository.Options) { o.Region = region })
	return scanServerlessRepoApplications(ctx, client, acct, region, st, scanID)
}

func scanServerlessRepoApplications(ctx context.Context, client serverlessRepoAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := serverlessapplicationrepository.NewListApplicationsPaginator(client, &serverlessapplicationrepository.ListApplicationsInput{},
		func(o *serverlessapplicationrepository.ListApplicationsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "serverlessrepo:ListApplications", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("serverlessrepo:ListApplications: %w", perr)
		}
		for _, a := range out.Applications {
			arn := sv(a.ApplicationId)
			if arn == "" {
				continue
			}
			name := sv(a.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServerlessRepoApplication, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "serverlessrepo applications")
}
