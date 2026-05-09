package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/codeartifact"
)

func init() {
	registerService(serviceEntry{
		name: "aws:codeartifact",
		fn:   scanCodeArtifact,
		emits: []coverage.TypeDecl{
			{Service: "codeartifact", DiscoType: TypeCodeArtifactDomain},
			{Service: "codeartifact", DiscoType: TypeCodeArtifactRepository},
			{Service: "codeartifact", DiscoType: TypeCodeArtifactPackageGroup},
		},
	})
}

type codeArtifactAPI interface {
	ListDomains(context.Context, *codeartifact.ListDomainsInput, ...func(*codeartifact.Options)) (*codeartifact.ListDomainsOutput, error)
	ListRepositories(context.Context, *codeartifact.ListRepositoriesInput, ...func(*codeartifact.Options)) (*codeartifact.ListRepositoriesOutput, error)
	ListPackageGroups(context.Context, *codeartifact.ListPackageGroupsInput, ...func(*codeartifact.Options)) (*codeartifact.ListPackageGroupsOutput, error)
}

// scanCodeArtifact discovers domains, repositories, and package groups.
// Repositories listed account-wide via ListRepositories; package groups
// fan out per domain (ListPackageGroups requires Domain).
func scanCodeArtifact(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codeartifact.NewFromConfig(acct.cfg, func(o *codeartifact.Options) { o.Region = region })

	domainNames, t, i, ferr := scanCADomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCARepositories(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCAPackageGroups(ctx, client, domainNames, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanCADomains(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var names []string
	var nextToken *string
	for {
		out, err := client.ListDomains(ctx, &codeartifact.ListDomainsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "codeartifact:ListDomains", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("codeartifact:ListDomains: %w", err)
		}
		for _, d := range out.Domains {
			arn := sv(d.Arn)
			name := sv(d.Name)
			if arn == "" {
				continue
			}
			if name != "" {
				names = append(names, name)
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeArtifactDomain, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "codeartifact domains")
	return names, t, i, err
}

func scanCARepositories(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListRepositories(ctx, &codeartifact.ListRepositoriesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codeartifact:ListRepositories", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codeartifact:ListRepositories: %w", err)
		}
		for _, r := range out.Repositories {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeArtifactRepository, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codeartifact repositories")
}

func scanCAPackageGroups(ctx context.Context, client codeArtifactAPI, domainNames []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domainNames {
		domain := d
		var nextToken *string
		for {
			out, err := client.ListPackageGroups(ctx, &codeartifact.ListPackageGroupsInput{
				Domain:    &domain,
				NextToken: nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "codeartifact:ListPackageGroups", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("codeartifact:ListPackageGroups domain=%s: %w", domain, err)
			}
			for _, pg := range out.PackageGroups {
				arn := sv(pg.Arn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCodeArtifactPackageGroup, NativeID: arn,
					Name: pg.Pattern, Region: &region,
					AttributesJSON: mustJSON(pg), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "codeartifact package-groups")
}
