package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *codeartifact.ListTagsForResourceInput, ...func(*codeartifact.Options)) (*codeartifact.ListTagsForResourceOutput, error)
}

const (
	caTagsOp = "codeartifact:ListTagsForResource"
	// caPageCap is the maxResults maximum of every CodeArtifact listing read
	// here.
	caPageCap = 1000
)

// caGone is the not-found code ListTagsForResource models, and the one
// ListPackageGroups and ListPackages model for a domain or repository deleted
// since it was listed.
var caGone = []string{"ResourceNotFoundException"}

func scanCodeArtifact(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := codeartifact.NewFromConfig(acct.cfg, func(o *codeartifact.Options) { o.Region = region })
	return scanCodeArtifactAll(ctx, client, acct, region, st, scanID)
}

// scanCodeArtifactAll discovers the CodeArtifact resources of one region: the
// domains, the repositories, each domain's package groups and each
// repository's packages. Every listing runs as its own phase, one at a time:
// a phase's error does not stop the later ones; a failed write and ctx's end
// do (runHostPhases). The package-group and package phases read the parents
// stored this scan. Domains, repositories and package groups have their tags
// read per row (caTags), whose denials are one warning for the whole scan;
// packages have none (Untaggable).
func scanCodeArtifactAll(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := caTags(client).sharingDenials()
	var (
		domains []childParent
		repos   []caRepository
	)
	total, err := runHostPhases(ctx, st, acct, region, "codeartifact", 0, []hostPhase{
		{op: "codeartifact:ListDomains", run: func(p *phaseTotals) (failed bool) {
			domains, failed = scanCADomains(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "codeartifact:ListRepositories", run: func(p *phaseTotals) (failed bool) {
			repos, failed = scanCARepositories(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "codeartifact:ListPackageGroups", run: func(p *phaseTotals) bool {
			return scanCAPackageGroups(ctx, client, acct, region, st, scanID, domains, tags, p)
		}},
		countedPhase(0, "codeartifact:ListPackages", func() (int, error) {
			return scanCAPackages(ctx, client, acct, region, st, scanID, repos)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// caTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists domain,
// package-group and repository under ListTagsForResource; package it does
// not. The op returns every tag in one response. ResourceNotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
func caTags(client codeArtifactAPI) rowTagReader {
	return rowTagReader{
		op:       caTagsOp,
		notFound: caGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &codeartifact.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t catypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanCADomains stores the domains with their tags and returns the stored
// ones that have a name, the parents of the package-group phase. ListDomains
// returns only the domains this account owns, which a per-account quota
// bounds, so the listing is held whole. It reports whether the write failed,
// after which the scan must stop.
func scanCADomains(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "codeartifact:ListDomains", "domains", "Name",
		func(ctx context.Context, token *string) ([]catypes.DomainSummary, *string, error) {
			out, err := client.ListDomains(ctx, &codeartifact.ListDomainsInput{MaxResults: aws.Int32(caPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Domains, out.NextToken, nil
		},
		func(d catypes.DomainSummary) (*store.Resource, string, string) {
			return arnRow("domains", d.Arn, d.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeArtifactDomain, NativeID: sv(d.Arn),
				Name: d.Name, Region: &region, Status: nonEmptyPtr(string(d.Status)),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		},
		func(d catypes.DomainSummary) string { return sv(d.Name) },
		tags, p)
}

// caRepository is a stored repository: its ARN, and the DomainOwner
// ListPackages takes alongside the domain and repository names.
type caRepository struct{ arn, domainOwner string }

// scanCARepositories stores the repositories with their tags and returns the
// stored ones, the parents of the package phase. The repositories of every
// domain are listed together, with no small bound, so each page is written
// as soon as its tags are read. It reports whether a write failed, after
// which the scan must stop.
//
// A repository can sit in a domain another account owns. A repository whose
// ARN names another account gives no tags and no error on a denial or
// not-found (untaggedIfOtherAccount), so it is kept untagged without a
// warning. The ARN decides, not the element's AdministratorAccount: the ARN
// names the account the resource is in, so a denial on a repository whose
// ARN names this account is this account's own and warns, whoever
// administers it. Whether such a repository's ARN names its domain's owner,
// whether ListRepositories returns it, and whether the tag read is refused
// for it, is unverified live.
func scanCARepositories(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]caRepository, bool) {
	var (
		// listed holds the element of each row of the page being stored, so
		// that stored can carry its DomainOwner to the package phase.
		listed = map[*store.Resource]catypes.RepositorySummary{}
		repos  []caRepository
	)
	tags = tags.untaggedIfOtherAccount(acct.ID)
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "codeartifact:ListRepositories",
		func(ctx context.Context, token *string) ([]catypes.RepositorySummary, *string, error) {
			out, err := client.ListRepositories(ctx, &codeartifact.ListRepositoriesInput{MaxResults: aws.Int32(caPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Repositories, out.NextToken, nil
		},
		func(e catypes.RepositorySummary) (*store.Resource, string, string) {
			r, why, key := arnRow("repositories", e.Arn, e.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeArtifactRepository, NativeID: sv(e.Arn),
				Name: e.Name, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
			if r != nil {
				listed[r] = e
			}
			return r, why, key
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				repos = append(repos, caRepository{arn: r.NativeID, domainOwner: sv(listed[r].DomainOwner)})
			}
			clear(listed)
		}},
		tags, p)
	return repos, failed
}

// scanCAPackageGroups stores each stored domain's package groups with their
// tags, each page as soon as its tags are read (storeEachParentPagesTags):
// one domain's failure costs the others none of theirs, and a domain deleted
// since it was listed (ResourceNotFoundException) is skipped. The domains are
// this account's own (ListDomains), so each listing is sent no DomainOwner.
// It reports whether a write failed, after which the scan must stop.
func scanCAPackageGroups(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "codeartifact:ListPackageGroups", caGone, domains,
		func(ctx context.Context, domain string, token *string) ([]catypes.PackageGroupSummary, *string, error) {
			out, err := client.ListPackageGroups(ctx, &codeartifact.ListPackageGroupsInput{Domain: &domain, MaxResults: aws.Int32(caPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.PackageGroups, out.NextToken, nil
		},
		func(_ string, pg catypes.PackageGroupSummary) (*store.Resource, string, string) {
			return arnRow("package groups", pg.Arn, pg.Pattern, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeArtifactPackageGroup, NativeID: sv(pg.Arn),
				Name: pg.Pattern, Region: &region,
				AttributesJSON: mustJSON(pg), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanCAPackages lists the packages of each given repository, storing each
// page as it arrives because a repository's package count has no small bound
// (one with an external connection keeps every package fetched through it):
// it costs one ListPackages call per 1000 packages per repository,
// fanoutMed repositories at a time, and holds at most one page per repository
// in flight. A repository deleted after ListRepositories
// (ResourceNotFoundException) is skipped.
func scanCAPackages(ctx context.Context, client codeArtifactAPI, acct *account, region string, st *store.Store, scanID string, repos []caRepository) (int, error) {
	const op = "codeartifact:ListPackages"
	parents := make([]childParent, len(repos))
	owners := make(map[string]string, len(repos))
	for k, r := range repos {
		parents[k] = childParent{id: r.arn, arn: r.arn}
		owners[r.arn] = r.domainOwner
	}
	return childFanOutStoredSkipTransient(ctx, st, acct, region, op, parents, isCANotFound, byParentARN,
		func(ctx context.Context, p childParent) (int, error) {
			repo, err := parseCARepositoryARN(p.arn)
			if err != nil {
				return 0, err
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			pager := codeartifact.NewListPackagesPaginator(client, &codeartifact.ListPackagesInput{
				Domain: &repo.domain, Repository: &repo.name, DomainOwner: nonEmptyPtr(owners[p.arn]),
			}, func(o *codeartifact.ListPackagesPaginatorOptions) {
				o.Limit = caPageCap
				o.StopOnDuplicateToken = true
			})
			var total int
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, err
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
				t, err := upsertContained(st, op, page)
				total += t
				if err != nil {
					return total, err
				}
			}
			return total, nil
		})
}

func isCANotFound(err error) bool { return isAPIErrorCode(err, caGone...) }

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
