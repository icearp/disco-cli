package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

const (
	ecrRepositoriesOp = "ecr:DescribeRepositories"
	ecrListTagsOp     = "ecr:ListTagsForResource"
	// ecrPageLimit is the model's cap on MaxResults (range 1..1000) for every
	// ECR and ECR Public listing that sets it.
	ecrPageLimit int32 = 1000
)

// ecrAPI is the narrow set of ECR operations called by scanECRRepositories.
type ecrAPI interface {
	DescribeRepositories(context.Context, *ecr.DescribeRepositoriesInput, ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	ListTagsForResource(context.Context, *ecr.ListTagsForResourceInput, ...func(*ecr.Options)) (*ecr.ListTagsForResourceOutput, error)
}

// scanECR runs the region's ECR phases in order. Each phase runs whatever
// error an earlier phase returned, except a failed store write, which stops
// the scan; the scanning-configuration phase gets the repositories the first
// phase stored.
func scanECR(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ecr.NewFromConfig(acct.cfg, func(o *ecr.Options) { o.Region = region })

	var p phaseTotals
	repos, stop := scanECRRepositories(ctx, client, acct, region, st, scanID, &p)
	if stop {
		return p.result(st, acct, region)
	}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{"ecr:DescribePullThroughCacheRules", func() (int, int, error) {
			return scanECRPullThroughCacheRules(ctx, client, acct, region, st, scanID)
		}},
		{"ecr:ListPullTimeUpdateExclusions", func() (int, int, error) {
			return scanECRPullTimeUpdateExclusions(ctx, client, acct, region, st, scanID)
		}},
		{"ecr:GetRegistryPolicy", func() (int, int, error) {
			return scanECRRegistryPolicy(ctx, client, acct, region, st, scanID)
		}},
		{"ecr:GetRegistryScanningConfiguration", func() (int, int, error) {
			return scanECRRegistryScanningConfig(ctx, client, acct, region, st, scanID)
		}},
		{"ecr:DescribeRegistry", func() (int, int, error) {
			return scanECRReplicationConfiguration(ctx, client, acct, region, st, scanID)
		}},
		{"ecr:DescribeRepositoryCreationTemplates", func() (int, int, error) {
			return scanECRRepositoryCreationTemplates(ctx, client, acct, region, st, scanID)
		}},
		{"ecr:GetSigningConfiguration", func() (int, int, error) {
			return scanECRSigningConfiguration(ctx, client, acct, region, st, scanID)
		}},
	} {
		t, i, err := phase.run()
		if p.addPhase(phase.op, t, i, err) {
			return p.result(st, acct, region)
		}
	}
	if scanECRPublic(ctx, acct, region, st, scanID, &p) {
		return p.result(st, acct, region)
	}
	t, i, err := scanECRRepositoryScanningConfigs(ctx, client, acct, region, st, scanID, repos)
	p.addPhase("ecr:BatchGetRepositoryScanningConfiguration", t, i, err)
	return p.result(st, acct, region)
}

// scanECRRepositories stores the region's repositories, each with the tags
// ListTagsForResource reads for it (DescribeRepositories returns none), and
// returns the stored ones that have a name (id: name, arn: NativeID), the
// parents of the scanning-configuration phase. A repository the tag read no
// longer finds was deleted since it was listed and is not stored. Counts and
// errors go to p as storeParentListTags says; stop reports a failed write.
func scanECRRepositories(ctx context.Context, client ecrAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (repos []childParent, stop bool) {
	return storeParentListTags(ctx, st, acct, region, ecrRepositoriesOp, "repositories", "RepositoryName",
		func(ctx context.Context, token *string) ([]ecrtypes.Repository, *string, error) {
			out, err := client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
				NextToken: token, MaxResults: sdkaws.Int32(ecrPageLimit),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Repositories, out.NextToken, nil
		},
		func(repo ecrtypes.Repository) (*store.Resource, string, string) {
			arn := sv(repo.RepositoryArn)
			if arn == "" {
				return nil, "repositories have an empty RepositoryArn", cmp.Or(sv(repo.RepositoryName), "(a repository with no RepositoryName)")
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeECRRepository,
				NativeID:       arn,
				Name:           repo.RepositoryName,
				Region:         &region,
				CreatedAt:      tp(repo.CreatedAt),
				AttributesJSON: mustJSON(repo),
				DiscoveredBy:   scanID,
			}, "", ""
		},
		func(repo ecrtypes.Repository) string { return sv(repo.RepositoryName) },
		rowTagReader{
			op:       ecrListTagsOp,
			notFound: []string{"RepositoryNotFoundException"},
			read: func(ctx context.Context, r *store.Resource) (*string, error) {
				out, err := client.ListTagsForResource(ctx, &ecr.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
				if err != nil {
					return nil, err
				}
				return awsTagsJSON(out.Tags), nil
			},
		}, p)
}
