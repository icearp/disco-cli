package aws

import (
	"context"
	"fmt"
	"strings"

	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeArtifactDomain, Service: "codeartifact"})
	registerType(restype.Descriptor{Type: TypeCodeArtifactRepository, Service: "codeartifact"})
	registerType(restype.Descriptor{Type: TypeCodeArtifactPackageGroup, Service: "codeartifact"})
	registerType(restype.Descriptor{Type: TypeCodeArtifactPackage, Service: "codeartifact", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:codeartifact",
		fn:   scanCodeArtifact,
	})
}

type codeArtifactAPI interface {
	ListDomains(context.Context, *codeartifact.ListDomainsInput, ...func(*codeartifact.Options)) (*codeartifact.ListDomainsOutput, error)
	ListRepositories(context.Context, *codeartifact.ListRepositoriesInput, ...func(*codeartifact.Options)) (*codeartifact.ListRepositoriesOutput, error)
	ListPackageGroups(context.Context, *codeartifact.ListPackageGroupsInput, ...func(*codeartifact.Options)) (*codeartifact.ListPackageGroupsOutput, error)
	ListPackages(context.Context, *codeartifact.ListPackagesInput, ...func(*codeartifact.Options)) (*codeartifact.ListPackagesOutput, error)
}

// codeArtifactPackagePageLimit is ListPackagesMaxResults' model maximum.
const codeArtifactPackagePageLimit = 1000

func scanCodeArtifact(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := codeartifact.NewFromConfig(acct.cfg, func(o *codeartifact.Options) { o.Region = region })
	return scanCodeArtifactAll(ctx, client, acct, region, st, scanID)
}

// scanCodeArtifactAll runs the domain, repository and package-group phases,
// which stop at their first hard error, then lists the packages of the
// repositories stored before that error, so a failure on either side costs the
// other no rows. The first side's transient error is demoted to a warning only
// beside a hard error: joined, it would be buried in the hard error instead of
// reported under its op.
func scanCodeArtifactAll(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, repos, err := scanCAParents(ctx, client, acct, region, st, scanID)
	t, i, perr := scanCAPackages(ctx, client, acct, region, st, scanID, repos)
	return total + t, inserted + i, demoteBeside(st, "codeartifact", acct, region, err, perr)
}

// scanCAParents stores domains, repositories and package groups, stopping at
// the first hard error, and returns the repositories stored.
// Repositories are listed account-wide via ListRepositories; package groups
// fan out per domain (ListPackageGroups requires Domain).
func scanCAParents(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, repos []caRepository, err error) {
	domainNames, t, i, err := scanCADomains(ctx, client, acct, region, st, scanID)
	if err != nil {
		return 0, 0, nil, err
	}
	total, inserted = total+t, inserted+i

	repos, t, i, err = scanCARepositories(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, nil, err
	}
	total, inserted = total+t, inserted+i

	t, i, err = scanCAPackageGroups(ctx, client, domainNames, acct, region, st, scanID)
	return total + t, inserted + i, repos, err
}

// caRepository is a stored repository: its ARN, and the DomainOwner
// ListPackages takes alongside the domain and repository names.
type caRepository struct{ arn, domainOwner string }

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

// scanCARepositories stores the repositories and, once they are stored,
// returns them.
func scanCARepositories(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string) ([]caRepository, int, int, error) {
	var (
		batch []*store.Resource
		repos []caRepository
	)
	var nextToken *string
	for {
		out, err := client.ListRepositories(ctx, &codeartifact.ListRepositoriesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "codeartifact:ListRepositories", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("codeartifact:ListRepositories: %w", err)
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
			repos = append(repos, caRepository{arn: arn, domainOwner: sv(r.DomainOwner)})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "codeartifact repositories")
	if err != nil {
		return nil, 0, 0, err
	}
	return repos, t, i, nil
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

// scanCAPackages lists the packages of each given repository, storing each
// page as it arrives because a repository's package count has no small bound
// (one with an external connection keeps every package fetched through it):
// it costs one ListPackages call per 1000 packages per repository,
// fanoutMed repositories at a time, and holds at most one page per repository
// in flight. A repository deleted after ListRepositories
// (ResourceNotFoundException) is skipped.
func scanCAPackages(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string, repos []caRepository) (int, int, error) {
	const op = "codeartifact:ListPackages"
	parents := make([]childParent, len(repos))
	owners := make(map[string]string, len(repos))
	for k, r := range repos {
		parents[k] = childParent{id: r.arn, arn: r.arn}
		owners[r.arn] = r.domainOwner
	}
	return childFanOutStoredSkipTransient(ctx, st, acct, region, op, parents, isCANotFound, byParentARN,
		func(ctx context.Context, p childParent) (int, int, error) {
			repo, err := parseCARepositoryARN(p.arn)
			if err != nil {
				return 0, 0, err
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			pager := codeartifact.NewListPackagesPaginator(client, &codeartifact.ListPackagesInput{
				Domain: &repo.domain, Repository: &repo.name, DomainOwner: nonEmptyPtr(owners[p.arn]),
			}, func(o *codeartifact.ListPackagesPaginatorOptions) {
				o.Limit = codeArtifactPackagePageLimit
				o.StopOnDuplicateToken = true
			})
			var total, inserted int
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				page := make([]containedChild, 0, len(out.Packages))
				for _, pkg := range out.Packages {
					if pkg.Format == "" || sv(pkg.Package) == "" {
						continue
					}
					page = append(page, containedChild{parentID: parentID, row: &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeCodeArtifactPackage, NativeID: repo.packageARN(pkg),
						Name: pkg.Package, Region: &region,
						AttributesJSON: mustJSON(pkg), DiscoveredBy: scanID,
					}})
				}
				t, i, err := upsertContained(st, op, page)
				total, inserted = total+t, inserted+i
				if err != nil {
					return total, inserted, err
				}
			}
			return total, inserted, nil
		})
}

func isCANotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// caRepositoryARN is a repository ARN split into what ListPackages and the
// package ARN need.
type caRepositoryARN struct {
	arn          awsarn.ARN
	domain, name string
}

// parseCARepositoryARN splits arn:<partition>:codeartifact:<region>:<account>:repository/<domain>/<name>.
// The model patterns of DomainName and RepositoryName exclude "/".
func parseCARepositoryARN(s string) (caRepositoryARN, error) {
	a, err := awsarn.Parse(s)
	if err != nil {
		return caRepositoryARN{}, err
	}
	parts := strings.Split(a.Resource, "/")
	if len(parts) != 3 || parts[0] != "repository" || parts[1] == "" || parts[2] == "" {
		return caRepositoryARN{}, fmt.Errorf("repository ARN %q is not repository/<domain>/<name>", s)
	}
	return caRepositoryARN{arn: a, domain: parts[1], name: parts[2]}, nil
}

// packageARN builds the package's ARN in the Service Reference format
// package/<domain>/<repository>/<format>/<namespace>/<name>, in the
// repository's partition, region and account. ListPackages returns no ARN.
// The model pattern of PackageNamespace and PackageName excludes "/", and a
// package without a namespace (pypi, nuget, ruby, cargo, unscoped npm) keeps
// an empty segment, so the segments stay positional.
func (r caRepositoryARN) packageARN(p catypes.PackageSummary) string {
	a := r.arn
	a.Resource = strings.Join([]string{"package", r.domain, r.name, string(p.Format), sv(p.Namespace), sv(p.Package)}, "/")
	return a.String()
}
