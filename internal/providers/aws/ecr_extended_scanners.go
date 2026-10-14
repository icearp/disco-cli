package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	ecrpublictypes "github.com/aws/aws-sdk-go-v2/service/ecrpublic/types"
	"github.com/icearp/disco-cli/store"
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
