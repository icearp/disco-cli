package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/codecommit"
)

func init() {
	registerService(serviceEntry{
		name: "aws:codecommit",
		fn:   scanCodeCommit,
		emits: []coverage.TypeDecl{
			{Service: "codecommit", DiscoType: TypeCodeCommitRepository},
		},
	})
}

type codeCommitAPI interface {
	ListRepositories(context.Context, *codecommit.ListRepositoriesInput, ...func(*codecommit.Options)) (*codecommit.ListRepositoriesOutput, error)
}

// scanCodeCommit discovers CodeCommit repositories. CodeCommit is closed
// to new customers (2024) but existing customer accounts continue to use
// it. Synth ARN: arn:aws:codecommit:{r}:{a}:{name}.
func scanCodeCommit(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codecommit.NewFromConfig(acct.cfg, func(o *codecommit.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListRepositories(ctx, &codecommit.ListRepositoriesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codecommit:ListRepositories", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codecommit:ListRepositories: %w", err)
		}
		for _, r := range out.Repositories {
			name := sv(r.RepositoryName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:codecommit:%s:%s:%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeCommitRepository, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codecommit repositories")
}
