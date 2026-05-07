package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/codegurureviewer"
)

func init() {
	registerService(serviceEntry{
		name: "aws:code-guru-reviewer",
		fn:   scanCodeGuruReviewer,
		emits: []coverage.TypeDecl{
			{Service: "code-guru-reviewer", DiscoType: TypeCodeGuruReviewerRepositoryAssociation},
		},
	})
}

// scanCodeGuruReviewer discovers CodeGuru Reviewer repository associations.
func scanCodeGuruReviewer(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codegurureviewer.NewFromConfig(acct.cfg, func(o *codegurureviewer.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListRepositoryAssociations(ctx, &codegurureviewer.ListRepositoryAssociationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codeguru-reviewer:ListRepositoryAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codeguru-reviewer:ListRepositoryAssociations: %w", err)
		}
		for _, a := range out.RepositoryAssociationSummaries {
			arn := sv(a.AssociationArn)
			if arn == "" {
				continue
			}
			status := string(a.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeGuruReviewerRepositoryAssociation, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "code-guru-reviewer repository-associations")
}
