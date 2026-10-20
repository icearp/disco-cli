package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeECRRepository, Service: "ecr"})
	registerType(restype.Descriptor{Type: TypeECRPublicRepository, Service: "ecr"})
	registerType(restype.Descriptor{Type: TypeECRPublicRegistry, Service: "ecr", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeECRPullThroughCacheRule, Service: "ecr"})
	registerType(restype.Descriptor{Type: TypeECRPullTimeUpdateExclusion, Service: "ecr"})
	registerType(restype.Descriptor{Type: TypeECRRegistryPolicy, Service: "ecr"})
	registerType(restype.Descriptor{Type: TypeECRRegistryScanningConfig, Service: "ecr", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeECRReplicationConfiguration, Service: "ecr", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeECRRepositoryCreationTemplate, Service: "ecr"})
	// Every repository has one, computed by ECR from the registry scanning
	// rules (or the repository's own scan-on-push flag), with no Delete API.
	// Still not Managed: those rules and flags are the customer's choice, and
	// Managed rows are left out of default `disco check` input.
	registerType(restype.Descriptor{Type: TypeECRRepositoryScanningConfig, Service: "ecr", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeECRSigningConfiguration, Service: "ecr"})
	registerService(serviceEntry{
		name: "aws:ecr",
		fn:   scanECR,
	})
}

// ecrAPI is the narrow set of ECR operations called by scanECRRepositories.
type ecrAPI interface {
	DescribeRepositories(context.Context, *ecr.DescribeRepositoriesInput, ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	ListTagsForResource(context.Context, *ecr.ListTagsForResourceInput, ...func(*ecr.Options)) (*ecr.ListTagsForResourceOutput, error)
}

// scanECR runs the region's ECR phases in order and stops at the first error.
func scanECR(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ecr.NewFromConfig(acct.cfg, func(o *ecr.Options) { o.Region = region })

	var repos []childParent
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			t, i, repos, err = scanECRRepositories(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return scanECRPullThroughCacheRules(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanECRPullTimeUpdateExclusions(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanECRRegistryPolicy(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanECRRegistryScanningConfig(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanECRReplicationConfiguration(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanECRRepositoryCreationTemplates(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanECRSigningConfiguration(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanECRPublic(ctx, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanECRRepositoryScanningConfigs(ctx, client, acct, region, st, scanID, repos)
		},
	} {
		t, i, ferr := phase()
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

// scanECRRepositories stores the region's repositories and returns the stored
// ones that have a name (id: name, arn: NativeID), the parents of the
// scanning-configuration phase.
func scanECRRepositories(ctx context.Context, client ecrAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, repos []childParent, err error) {
	pager := ecr.NewDescribeRepositoriesPaginator(client, &ecr.DescribeRepositoriesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "ecr:DescribeRepositories", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("ecr:DescribeRepositories: %w", err)
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
			return 0, 0, nil, err
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
				return 0, 0, nil, fmt.Errorf("upsert ECR repositories: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		for _, r := range batch {
			if name := sv(r.Name); name != "" {
				repos = append(repos, childParent{id: name, arn: r.NativeID})
			}
		}
	}
	return total, inserted, repos, nil
}
