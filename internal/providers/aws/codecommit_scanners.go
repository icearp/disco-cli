package aws

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/codecommit"
	codecommittypes "github.com/aws/aws-sdk-go-v2/service/codecommit/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeCommitRepository, Service: "codecommit"})
	registerType(restype.Descriptor{Type: TypeCodeCommitApprovalRuleTemplate, Service: "codecommit"})
	registerType(restype.Descriptor{Type: TypeCodeCommitRepositoryTrigger, Service: "codecommit"})
	registerService(serviceEntry{
		name: "aws:codecommit",
		fn:   scanCodeCommit,
	})
}

type codeCommitAPI interface {
	ListRepositories(context.Context, *codecommit.ListRepositoriesInput, ...func(*codecommit.Options)) (*codecommit.ListRepositoriesOutput, error)
	GetRepository(context.Context, *codecommit.GetRepositoryInput, ...func(*codecommit.Options)) (*codecommit.GetRepositoryOutput, error)
	ListApprovalRuleTemplates(context.Context, *codecommit.ListApprovalRuleTemplatesInput, ...func(*codecommit.Options)) (*codecommit.ListApprovalRuleTemplatesOutput, error)
	GetApprovalRuleTemplate(context.Context, *codecommit.GetApprovalRuleTemplateInput, ...func(*codecommit.Options)) (*codecommit.GetApprovalRuleTemplateOutput, error)
	ListRepositoriesForApprovalRuleTemplate(context.Context, *codecommit.ListRepositoriesForApprovalRuleTemplateInput, ...func(*codecommit.Options)) (*codecommit.ListRepositoriesForApprovalRuleTemplateOutput, error)
	GetRepositoryTriggers(context.Context, *codecommit.GetRepositoryTriggersInput, ...func(*codecommit.Options)) (*codecommit.GetRepositoryTriggersOutput, error)
}

// codeCommitRepoARN is the repository ARN; CodeCommit returns it only from
// GetRepository, and resolvers rebuild it from a repository name.
func codeCommitRepoARN(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:codecommit:%s:%s:%s", region, acctID, name)
}

// codeCommitTemplateNativeID keys an approval rule template, which has no ARN,
// on its system-generated id: the name can be changed in place
// (UpdateApprovalRuleTemplateName). A repository name cannot contain "/", so
// the id never collides with a repository ARN.
func codeCommitTemplateNativeID(region, acctID, templateID string) string {
	return fmt.Sprintf("arn:aws:codecommit:%s:%s:approval-rule-template/%s", region, acctID, templateID)
}

// scanCodeCommit discovers CodeCommit repositories, approval rule templates
// and repository triggers.
func scanCodeCommit(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codecommit.NewFromConfig(acct.cfg, func(o *codecommit.Options) { o.Region = region })
	return scanCodeCommitIn(ctx, client, acct, region, st, scanID)
}

// scanCodeCommitIn runs every phase even when an earlier one fails, so one
// phase's error costs no other phase its rows. Triggers fan out over the
// repositories listed first (none when that listing failed).
func scanCodeCommitIn(ctx context.Context, client codeCommitAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		repos []childParent
		errs  phaseErrs
	)
	for n, phase := range []struct {
		name string
		run  func() (int, int, error)
	}{
		{"codecommit:ListRepositories", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			repos, t, i, err = scanCCRepositories(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"codecommit:ListApprovalRuleTemplates", func() (int, int, error) {
			return scanCCApprovalRuleTemplates(ctx, client, acct, region, st, scanID)
		}},
		{"codecommit:GetRepositoryTriggers", func() (int, int, error) {
			return scanCCRepositoryTriggers(ctx, client, acct, region, st, scanID, repos)
		}},
	} {
		t, i, perr := phase.run()
		if n == 0 && isEndpointUnreachable(perr) {
			return t, i, perr
		}
		total += t
		inserted += i
		errs.add(phase.name, lateNXDOMAIN(phase.name, perr))
	}
	return total, inserted, errs.join(st, acct, region)
}

// scanCCRepositories returns the stored repositories as trigger parents (id is
// the repository name GetRepositoryTriggers keys on).
func scanCCRepositories(ctx context.Context, client codeCommitAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var (
		batch     []*store.Resource
		repos     []childParent
		nextToken *string
	)
	for {
		out, err := client.ListRepositories(ctx, &codecommit.ListRepositoriesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "codecommit:ListRepositories", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("codecommit:ListRepositories: %w", err)
		}
		for _, r := range out.Repositories {
			name := sv(r.RepositoryName)
			if name == "" {
				continue
			}
			arn := codeCommitRepoARN(region, acct.ID, name)
			attrsJSON := mustJSON(r)
			if gout, gerr := client.GetRepository(ctx, &codecommit.GetRepositoryInput{RepositoryName: r.RepositoryName}); gerr == nil && gout.RepositoryMetadata != nil {
				attrsJSON = mustJSON(gout.RepositoryMetadata)
			}
			repos = append(repos, childParent{id: name, arn: arn})
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
	t, i, err := upsertBatch(st, batch, "codecommit repositories")
	if err != nil {
		return nil, 0, 0, err
	}
	return repos, t, i, nil
}

// codeCommitTemplateAttrs is the template body plus the names of the
// repositories it is associated with. AssociatedRepositoryNames is null when
// the association listing was denied and [] when the template has none.
type codeCommitTemplateAttrs struct {
	codecommittypes.ApprovalRuleTemplate
	AssociatedRepositoryNames []string
}

// ccEncryptionKeyCodes are the errors CodeCommit returns when it cannot use a
// repository's KMS key. Each concerns that one repository's key.
var ccEncryptionKeyCodes = []string{
	"EncryptionKeyAccessDeniedException", "EncryptionKeyDisabledException", "EncryptionKeyNotFoundException",
	"EncryptionKeyUnavailableException", "EncryptionIntegrityChecksFailedException",
}

// ccKeyWarner warns once for op about a repository whose KMS key CodeCommit
// cannot use, so one broken key costs only that repository's data rather than
// failing the whole fan-out.
type ccKeyWarner struct {
	once   sync.Once
	st     *store.Store
	acct   *account
	region string
	op     string
}

// skip reports whether err is an encryption-key error, warning on the first.
func (w *ccKeyWarner) skip(err error) bool {
	if !isAPIErrorCode(err, ccEncryptionKeyCodes...) {
		return false
	}
	w.once.Do(func() { _ = skipIfAccessDenied(w.st, w.op, w.acct.ID, w.region, err) })
	return true
}

// isCCTemplateGone reports a template deleted between the list and a later
// call naming it.
func isCCTemplateGone(err error) bool {
	return isAPIErrorCode(err, "ApprovalRuleTemplateDoesNotExistException")
}

// scanCCApprovalRuleTemplates stores the region's approval rule templates. The
// list returns names only, so each template is fetched for its id and content,
// and its associated repositories are listed per template: an account has far
// fewer templates than repositories, so this is the cheaper direction.
func scanCCApprovalRuleTemplates(ctx context.Context, client codeCommitAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var names []string
	pager := codecommit.NewListApprovalRuleTemplatesPaginator(client, &codecommit.ListApprovalRuleTemplatesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codecommit:ListApprovalRuleTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codecommit:ListApprovalRuleTemplates: %w", err)
		}
		names = append(names, out.ApprovalRuleTemplateNames...)
	}
	const assocOp = "codecommit:ListRepositoriesForApprovalRuleTemplate"
	assocErrs := &fanOutErrs{st: st, acct: acct, region: region, op: assocOp, isSkip: func(error) bool { return false }}
	assocKeyErrs := &ccKeyWarner{st: st, acct: acct, region: region, op: assocOp}
	rows, err := describeFanOut(ctx, st, acct, region, "codecommit:GetApprovalRuleTemplate", names, isCCTemplateGone,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.GetApprovalRuleTemplate(ctx, &codecommit.GetApprovalRuleTemplateInput{ApprovalRuleTemplateName: &name})
			if err != nil {
				return nil, err
			}
			tmpl := out.ApprovalRuleTemplate
			if tmpl == nil || sv(tmpl.ApprovalRuleTemplateId) == "" {
				return nil, nil
			}
			// A template deleted after the Get comes back as an error here,
			// which describeFanOut's isCCTemplateGone then drops. A denial or
			// an associated repository's broken KMS key keeps the template
			// with a null association list.
			repoNames, err := listCCTemplateRepositories(ctx, client, name)
			if err != nil && !assocKeyErrs.skip(err) {
				if kerr := assocErrs.keyErr(name, err); kerr != nil {
					return nil, kerr
				}
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeCodeCommitApprovalRuleTemplate,
				NativeID: codeCommitTemplateNativeID(region, acct.ID, sv(tmpl.ApprovalRuleTemplateId)),
				Name:     tmpl.ApprovalRuleTemplateName, Region: &region, CreatedAt: tp(tmpl.CreationDate),
				AttributesJSON: mustJSON(codeCommitTemplateAttrs{ApprovalRuleTemplate: *tmpl, AssociatedRepositoryNames: repoNames}),
				DiscoveredBy:   scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, rows, "codecommit approval rule templates")
}

// listCCTemplateRepositories returns the names of the repositories associated
// with the template, sorted because the API documents no order and a reordered
// list would otherwise version the template.
func listCCTemplateRepositories(ctx context.Context, client codeCommitAPI, templateName string) ([]string, error) {
	names := []string{}
	pager := codecommit.NewListRepositoriesForApprovalRuleTemplatePaginator(client,
		&codecommit.ListRepositoriesForApprovalRuleTemplateInput{ApprovalRuleTemplateName: &templateName})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, out.RepositoryNames...)
	}
	slices.Sort(names)
	return names, nil
}

// isCCRepositoryGone reports a repository deleted between the list and a
// later call naming it.
func isCCRepositoryGone(err error) bool {
	return isAPIErrorCode(err, "RepositoryDoesNotExistException")
}

// scanCCRepositoryTriggers stores each repository's triggers under it, keyed
// by trigger name: the API returns no other identifier.
func scanCCRepositoryTriggers(ctx context.Context, client codeCommitAPI, acct *account, region string, st *store.Store, scanID string, repos []childParent) (int, int, error) {
	const op = "codecommit:GetRepositoryTriggers"
	keyErrs := &ccKeyWarner{st: st, acct: acct, region: region, op: op}
	isSkip := func(err error) bool { return isCCRepositoryGone(err) || keyErrs.skip(err) }
	return childFanOut(ctx, st, acct, region, op, repos, isSkip,
		func(ctx context.Context, repo childParent) ([]*store.Resource, error) {
			out, err := client.GetRepositoryTriggers(ctx, &codecommit.GetRepositoryTriggersInput{RepositoryName: &repo.id})
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, tr := range out.Triggers {
				name := sv(tr.Name)
				if name == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCodeCommitRepositoryTrigger, NativeID: codeCommitTriggerNativeID(repo.arn, name),
					Name: &name, Region: &region,
					AttributesJSON: mustJSON(tr), DiscoveredBy: scanID,
				})
			}
			return rows, nil
		})
}

func codeCommitTriggerNativeID(repoARN, name string) string {
	return repoARN + "/trigger/" + name
}
