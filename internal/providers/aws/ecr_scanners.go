package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:ecr", fn: scanECR}) }

// ecrAPI is the narrow set of ECR operations called by scanECRRepositories.
type ecrAPI interface {
	DescribeRepositories(context.Context, *ecr.DescribeRepositoriesInput, ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	ListTagsForResource(context.Context, *ecr.ListTagsForResourceInput, ...func(*ecr.Options)) (*ecr.ListTagsForResourceOutput, error)
}

// scanECR discovers ECR repositories in one region. DescribeRepositories returns
// full repository details in a single paginated call — no separate describe step needed.
// Tags are fetched concurrently via ListTagsForResource (one call per repository).
func scanECR(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ecr.NewFromConfig(acct.cfg, func(o *ecr.Options) { o.Region = region })
	return scanECRRepositories(ctx, client, acct, region, st, scanID)
}

// scanECRRepositories holds the testable scan body.
func scanECRRepositories(ctx context.Context, client ecrAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := ecr.NewDescribeRepositoriesPaginator(client, &ecr.DescribeRepositoriesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ecr:DescribeRepositories", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ecr:DescribeRepositories: %w", err)
		}

		// ECR does not return tags from DescribeRepositories; fetch them via
		// ListTagsForResource concurrently to avoid N sequential API calls.
		var mu sync.Mutex
		tagsByARN := make(map[string]*string, len(page.Repositories))
		g, gctx := errgroup.WithContext(ctx)
		for _, repo := range page.Repositories {
			arn := sv(repo.RepositoryArn)
			g.Go(func() error {
				out, err := client.ListTagsForResource(gctx, &ecr.ListTagsForResourceInput{ResourceArn: &arn})
				if err != nil {
					return nil // tags are best-effort; skip rather than fail the scan
				}
				if t := awsTagsJSON(out.Tags); t != nil {
					mu.Lock()
					tagsByARN[arn] = t
					mu.Unlock()
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}

		var batch []*store.Resource
		for _, repo := range page.Repositories {
			arn := sv(repo.RepositoryArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeECRRepository,
				NativeID:       arn,
				Name:           repo.RepositoryName,
				Region:         &region,
				CreatedAt:      tp(repo.CreatedAt),
				AttributesJSON: mustJSON(repo),
				TagsJSON:       tagsByARN[arn],
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert ECR repositories: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
