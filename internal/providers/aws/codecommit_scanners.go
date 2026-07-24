package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/codecommit"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeCommitRepository, Service: "codecommit"})
	registerService(serviceEntry{
		name: "aws:codecommit",
		fn:   scanCodeCommit,
	})
}

// scanCodeCommit discovers CodeCommit repositories. Closed to new
// customers since 2024; existing accounts still use it. Synth ARN:
// arn:aws:codecommit:{r}:{a}:{name}.
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
			attrsJSON := mustJSON(r)
			if gout, gerr := client.GetRepository(ctx, &codecommit.GetRepositoryInput{RepositoryName: r.RepositoryName}); gerr == nil && gout.RepositoryMetadata != nil {
				attrsJSON = mustJSON(gout.RepositoryMetadata)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeCommitRepository, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codecommit repositories")
}
