package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	ecrpublictypes "github.com/aws/aws-sdk-go-v2/service/ecrpublic/types"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

// ecrExtAPI lists the ECR ops used by the extended scanner phases: registry-
// scoped Get* ops return singleton config, Describe* ops are account-wide.
// Synthesized ARN format: arn:aws:ecr:{region}:{account}:{kind}/{key}.
type ecrExtAPI interface {
	DescribePullThroughCacheRules(context.Context, *ecr.DescribePullThroughCacheRulesInput, ...func(*ecr.Options)) (*ecr.DescribePullThroughCacheRulesOutput, error)
	ListPullTimeUpdateExclusions(context.Context, *ecr.ListPullTimeUpdateExclusionsInput, ...func(*ecr.Options)) (*ecr.ListPullTimeUpdateExclusionsOutput, error)
	GetRegistryPolicy(context.Context, *ecr.GetRegistryPolicyInput, ...func(*ecr.Options)) (*ecr.GetRegistryPolicyOutput, error)
	GetRegistryScanningConfiguration(context.Context, *ecr.GetRegistryScanningConfigurationInput, ...func(*ecr.Options)) (*ecr.GetRegistryScanningConfigurationOutput, error)
	DescribeRegistry(context.Context, *ecr.DescribeRegistryInput, ...func(*ecr.Options)) (*ecr.DescribeRegistryOutput, error)
	DescribeRepositoryCreationTemplates(context.Context, *ecr.DescribeRepositoryCreationTemplatesInput, ...func(*ecr.Options)) (*ecr.DescribeRepositoryCreationTemplatesOutput, error)
	GetSigningConfiguration(context.Context, *ecr.GetSigningConfigurationInput, ...func(*ecr.Options)) (*ecr.GetSigningConfigurationOutput, error)
	BatchGetRepositoryScanningConfiguration(context.Context, *ecr.BatchGetRepositoryScanningConfigurationInput, ...func(*ecr.Options)) (*ecr.BatchGetRepositoryScanningConfigurationOutput, error)
}

func ecrSingletonARN(region, acct, kind string) string {
	return fmt.Sprintf("arn:aws:ecr:%s:%s:%s", region, acct, kind)
}

func scanECRPullThroughCacheRules(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ecr.NewDescribePullThroughCacheRulesPaginator(client, &ecr.DescribePullThroughCacheRulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecr:DescribePullThroughCacheRules", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecr:DescribePullThroughCacheRules: %w", perr)
		}
		for _, r := range out.PullThroughCacheRules {
			prefix := sv(r.EcrRepositoryPrefix)
			if prefix == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ecr:%s:%s:pull-through-cache-rule/%s", region, acct.ID, prefix)
			label := prefix
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPullThroughCacheRule, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ecr pull-through-cache-rules")
}

func scanECRPullTimeUpdateExclusions(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// SDK exposes no paginator; use manual NextToken loop.
	input := &ecr.ListPullTimeUpdateExclusionsInput{}
	var batch []*store.Resource
	for {
		out, err := client.ListPullTimeUpdateExclusions(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ecr:ListPullTimeUpdateExclusions", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecr:ListPullTimeUpdateExclusions: %w", err)
		}
		for _, repoARN := range out.PullTimeUpdateExclusions {
			if repoARN == "" {
				continue
			}
			arn := fmt.Sprintf("%s/pull-time-update-exclusion", repoARN)
			label := repoARN
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPullTimeUpdateExclusion, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"EcrRepositoryArn": repoARN}), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ecr pull-time-update-exclusions")
}

func scanECRRegistryPolicy(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetRegistryPolicy(ctx, &ecr.GetRegistryPolicyInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ecr:GetRegistryPolicy", acct.ID, region, err)
		}
		// RegistryPolicyNotFoundException = no policy set (default state). Silent.
		if isAPIErrorCode(err, "RegistryPolicyNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("ecr:GetRegistryPolicy: %w", err)
	}
	if out == nil || sv(out.PolicyText) == "" {
		return 0, 0, nil
	}
	arn := ecrSingletonARN(region, acct.ID, "registry-policy")
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeECRRegistryPolicy, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "ecr registry-policy")
}

func scanECRRegistryScanningConfig(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetRegistryScanningConfiguration(ctx, &ecr.GetRegistryScanningConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, "ecr:GetRegistryScanningConfiguration", acct.ID, region, err)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("ecr:GetRegistryScanningConfiguration: %w", err)
	}
	arn := ecrSingletonARN(region, acct.ID, "registry-scanning-configuration")
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeECRRegistryScanningConfig, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		// Per-(acct,region) singleton registry-level scanning config.
	}}
	return upsertBatch(st, batch, "ecr registry-scanning-configuration")
}

func scanECRReplicationConfiguration(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeRegistry(ctx, &ecr.DescribeRegistryInput{})
	if err != nil {
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, "ecr:DescribeRegistry", acct.ID, region, err)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("ecr:DescribeRegistry: %w", err)
	}
	if out == nil || out.ReplicationConfiguration == nil {
		return 0, 0, nil
	}
	arn := ecrSingletonARN(region, acct.ID, "replication-configuration")
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeECRReplicationConfiguration, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out.ReplicationConfiguration), DiscoveredBy: scanID,
		// Per-(acct,region) singleton registry-level replication config.
	}}
	return upsertBatch(st, batch, "ecr replication-configuration")
}

func scanECRRepositoryCreationTemplates(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ecr.NewDescribeRepositoryCreationTemplatesPaginator(client, &ecr.DescribeRepositoryCreationTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecr:DescribeRepositoryCreationTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecr:DescribeRepositoryCreationTemplates: %w", perr)
		}
		for _, tmpl := range out.RepositoryCreationTemplates {
			prefix := sv(tmpl.Prefix)
			if prefix == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ecr:%s:%s:repository-creation-template/%s", region, acct.ID, prefix)
			label := prefix
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRRepositoryCreationTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(tmpl), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ecr repository-creation-templates")
}

func scanECRSigningConfiguration(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetSigningConfiguration(ctx, &ecr.GetSigningConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, "ecr:GetSigningConfiguration", acct.ID, region, err)
			return 0, 0, nil
		}
		// SigningConfigurationNotFoundException = no signing config set (default
		// state). Regions without image signing reject with ValidationException
		// "This feature is disabled". Both are default/availability state — no-op.
		if isAPIErrorCode(err, "SigningConfigurationNotFoundException") ||
			isAPIErrorWithMessage(err, "ValidationException", "feature is disabled") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("ecr:GetSigningConfiguration: %w", err)
	}
	arn := ecrSingletonARN(region, acct.ID, "signing-configuration")
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeECRSigningConfiguration, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "ecr signing-configuration")
}

// ecrScanningConfigBatchMax is the model's cap on the repositoryNames of one
// BatchGetRepositoryScanningConfiguration call.
const ecrScanningConfigBatchMax = 25

var errECRScanningConfigUnnamed = errors.New("response element has no repositoryName")

// ecrRepoScanningConfigNativeID is the scanning configuration's id, derived
// from its repository's ARN so that it keeps the repository's partition. The
// kind goes before the name, which may contain "/": a "{repoARN}/<kind>"
// suffix would collide with a repository named "<name>/<kind>".
func ecrRepoScanningConfigNativeID(repoARN, name string) (string, error) {
	a, err := arn.Parse(repoARN)
	if err != nil {
		return "", err
	}
	a.Resource = "repository-scanning-configuration/" + name
	return a.String(), nil
}

// scanECRRepositoryScanningConfigs stores the scanning configuration ECR
// reports for each of repos (the repositories the repository phase stored this
// scan), under its repository. It does not repeat the registry scanning
// configuration: that row holds the registry's rules, this one the frequency
// and the rule filters that apply to one repository.
//
// Per batch: see ecrScanningConfigBatch; any error it returns fails the
// phase. Per repository: failure code REPOSITORY_NOT_FOUND (deleted since it
// was listed) is dropped silently. Three other reasons drop a repository's
// row, each with one warning covering every repository it dropped: any other
// failure code, an element without a repositoryName, and an element whose
// repository has no usable stored ARN (none of that name in repos, or one
// that does not parse).
func scanECRRepositoryScanningConfigs(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string, repos []childParent) (int, int, error) {
	const op = "ecr:BatchGetRepositoryScanningConfiguration"
	repoARN := make(map[string]string, len(repos))
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		repoARN[r.id] = r.arn
		names = append(names, r.id)
	}
	var (
		mu                                 sync.Mutex
		children                           []containedChild
		failedRepos, unnamedEl, unparented skipTally
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: func(error) bool { return false }}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for batch := range slices.Chunk(names, ecrScanningConfigBatchMax) {
		g.Go(func() error {
			out, err := ecrScanningConfigBatch(gctx, client, batch, errs)
			if err != nil {
				return err
			}
			for _, f := range out.Failures {
				if f.FailureCode != ecrtypes.ScanningConfigurationFailureCodeRepositoryNotFound {
					failedRepos.skipped(sv(f.RepositoryName), fmt.Errorf("%s: %s", f.FailureCode, sv(f.FailureReason)))
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for _, c := range out.ScanningConfigurations {
				name := sv(c.RepositoryName)
				if name == "" {
					unnamedEl.skipped(sv(c.RepositoryArn), errECRScanningConfigUnnamed)
					continue
				}
				// A name not in repos maps to "", which fails the parse.
				parentARN := repoARN[name]
				nativeID, err := ecrRepoScanningConfigNativeID(parentARN, name)
				if err != nil {
					unparented.skipped(name, err)
					continue
				}
				children = append(children, containedChild{row: &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeECRRepositoryScanningConfig, NativeID: nativeID,
					Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				}, parentID: store.ResourceID("aws", acct.ID, parentARN)})
			}
			return nil
		})
	}
	err := g.Wait()
	failedRepos.warnCode(st, op, acct, region, "a failure code other than REPOSITORY_NOT_FOUND", "repository(ies)",
		"ECR reported a per-repository failure this scanner does not recognise")
	unnamedEl.warnCode(st, op, acct, region, "no repositoryName", "scanning configuration(s)",
		"the row's NativeID is built from it")
	unparented.warnCode(st, op, acct, region, "no usable repository ARN", "scanning configuration(s)",
		"the row's NativeID is built from its repository's ARN")
	if err != nil {
		return 0, 0, err
	}
	return upsertContained(st, op, children)
}

// ecrScanningConfigBatch gets the scanning configurations of names, returning
// an empty output for names dropped and an error only for one errs.keyErr
// does not drop (a denial warns once and is dropped).
//
// The model lists RepositoryNotFoundException for the whole call besides the
// per-repository REPOSITORY_NOT_FOUND failure. Whether one repository deleted
// since it was listed fails its whole batch that way is unverified, so on that
// code each name of a multi-name batch is asked alone (at most
// ecrScanningConfigBatchMax calls, on a rare path) and a name not found alone
// is dropped silently, like REPOSITORY_NOT_FOUND.
func ecrScanningConfigBatch(ctx context.Context, client ecrExtAPI, names []string, errs *fanOutErrs) (*ecr.BatchGetRepositoryScanningConfigurationOutput, error) {
	get := func(names []string) (*ecr.BatchGetRepositoryScanningConfigurationOutput, error) {
		return client.BatchGetRepositoryScanningConfiguration(ctx, &ecr.BatchGetRepositoryScanningConfigurationInput{RepositoryNames: names})
	}
	out, err := get(names)
	merged := &ecr.BatchGetRepositoryScanningConfigurationOutput{}
	switch {
	case err == nil:
		return out, nil
	case !isAPIErrorCode(err, "RepositoryNotFoundException"):
		return merged, errs.keyErr(fmt.Sprintf("batch of %d from %s", len(names), names[0]), err)
	case len(names) == 1:
		return merged, nil
	}
	for _, name := range names {
		one, err := get([]string{name})
		switch {
		case err == nil:
			merged.ScanningConfigurations = append(merged.ScanningConfigurations, one.ScanningConfigurations...)
			merged.Failures = append(merged.Failures, one.Failures...)
		case isAPIErrorCode(err, "RepositoryNotFoundException"):
		default:
			if kerr := errs.keyErr(name, err); kerr != nil {
				return nil, kerr
			}
		}
	}
	return merged, nil
}

// ecrPublicAPI lists the ECR Public ops scanECRPublic calls.
type ecrPublicAPI interface {
	DescribeRepositories(context.Context, *ecrpublic.DescribeRepositoriesInput, ...func(*ecrpublic.Options)) (*ecrpublic.DescribeRepositoriesOutput, error)
	DescribeRegistries(context.Context, *ecrpublic.DescribeRegistriesInput, ...func(*ecrpublic.Options)) (*ecrpublic.DescribeRegistriesOutput, error)
}

// scanECRPublic scans Public ECR repositories, then registries. ecrpublic is
// global but only callable from us-east-1 — gate other regions out so each
// account's rows are stored once.
func scanECRPublic(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if region != "us-east-1" {
		return 0, 0, nil
	}
	client := ecrpublic.NewFromConfig(acct.cfg, func(o *ecrpublic.Options) { o.Region = region })
	total, inserted, reposByRegistry, err := scanECRPublicRepositories(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, i, err := scanECRPublicRegistries(ctx, client, acct, region, st, scanID, reposByRegistry)
	return total + t, inserted + i, err
}

// scanECRPublicRepositories stores the public repositories and returns their
// ARNs keyed by RegistryId, for the registry phase's contains edges.
func scanECRPublicRepositories(ctx context.Context, client ecrPublicAPI, acct *account, region string, st *store.Store, scanID string) (int, int, map[string][]string, error) {
	pager := ecrpublic.NewDescribeRepositoriesPaginator(client, &ecrpublic.DescribeRepositoriesInput{})
	var batch []*store.Resource
	reposByRegistry := map[string][]string{}
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecr-public:DescribeRepositories", acct.ID, region, perr)
				return 0, 0, nil, nil
			}
			return 0, 0, nil, fmt.Errorf("ecr-public:DescribeRepositories: %w", perr)
		}
		for _, r := range out.Repositories {
			arn := sv(r.RepositoryArn)
			if arn == "" {
				continue
			}
			label := sv(r.RepositoryName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPublicRepository, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
			if regID := sv(r.RegistryId); regID != "" {
				reposByRegistry[regID] = append(reposByRegistry[regID], arn)
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "ecr public-repositories")
	if err != nil {
		return 0, 0, nil, err
	}
	return t, i, reposByRegistry, nil
}

// scanECRPublicRegistries stores the account's public registry and links it to
// the repositories the repository phase listed under the same RegistryId. A
// denial or error after a successful page keeps the rows listed so far.
func scanECRPublicRegistries(ctx context.Context, client ecrPublicAPI, acct *account, region string, st *store.Store, scanID string, reposByRegistry map[string][]string) (int, int, error) {
	const op = "ecr-public:DescribeRegistries"
	pager := ecrpublic.NewDescribeRegistriesPaginator(client, &ecrpublic.DescribeRegistriesInput{})
	var batch []*store.Resource
	var pairs [][2]string
	var listErr error
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			} else {
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		for _, r := range out.Registries {
			arn := sv(r.RegistryArn)
			if arn == "" {
				continue
			}
			regID := sv(r.RegistryId)
			label := ecrPublicRegistryLabel(r)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPublicRegistry, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
			parentID := store.ResourceID("aws", acct.ID, arn)
			for _, repoARN := range reposByRegistry[regID] {
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, repoARN), parentID})
			}
		}
	}
	t, i, uerr := upsertBatch(st, batch, "ecr public-registries")
	if uerr != nil {
		return 0, 0, errors.Join(listErr, uerr)
	}
	if herr := st.RecordHierarchyBatch(pairs); herr != nil {
		return t, i, errors.Join(listErr, fmt.Errorf("closure ecr public-registries: %w", herr))
	}
	return t, i, listErr
}

// ecrPublicRegistryLabel names a registry by its primary alias (the
// public.ecr.aws/<alias> path users see), else by its RegistryId.
func ecrPublicRegistryLabel(r ecrpublictypes.Registry) string {
	for _, a := range r.Aliases {
		if a.PrimaryRegistryAlias && sv(a.Name) != "" {
			return sv(a.Name)
		}
	}
	if id := sv(r.RegistryId); id != "" {
		return id
	}
	return sv(r.RegistryArn)
}
