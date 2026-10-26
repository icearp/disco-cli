package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
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
	BatchGetRepositoryScanningConfiguration(context.Context, *ecr.BatchGetRepositoryScanningConfigurationInput, ...func(*ecr.Options)) (*ecr.BatchGetRepositoryScanningConfigurationOutput, error)
}

func ecrSingletonARN(region, acct, kind string) string {
	return fmt.Sprintf("arn:aws:ecr:%s:%s:%s", region, acct, kind)
}

// scanECRPullThroughCacheRules stores the region's pull through cache rules,
// those read before a failed page included; a rule without an
// EcrRepositoryPrefix is warned about and not stored.
func scanECRPullThroughCacheRules(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "ecr:DescribePullThroughCacheRules",
		func(ctx context.Context, token *string) ([]ecrtypes.PullThroughCacheRule, *string, error) {
			out, err := client.DescribePullThroughCacheRules(ctx, &ecr.DescribePullThroughCacheRulesInput{
				NextToken: token, MaxResults: sdkaws.Int32(ecrPageLimit),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.PullThroughCacheRules, out.NextToken, nil
		},
		func(r ecrtypes.PullThroughCacheRule) (*store.Resource, string, string) {
			prefix := sv(r.EcrRepositoryPrefix)
			if prefix == "" {
				return nil, "pull through cache rules have an empty EcrRepositoryPrefix", cmp.Or(sv(r.UpstreamRegistryUrl), "(a rule with no UpstreamRegistryUrl)")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeECRPullThroughCacheRule,
				NativeID: fmt.Sprintf("arn:aws:ecr:%s:%s:pull-through-cache-rule/%s", region, acct.ID, prefix),
				Name:     &prefix, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanECRPullTimeUpdateExclusions stores the region's pull time update
// exclusions, those read before a failed page included; an empty repository
// ARN is warned about and not stored.
func scanECRPullTimeUpdateExclusions(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "ecr:ListPullTimeUpdateExclusions",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListPullTimeUpdateExclusions(ctx, &ecr.ListPullTimeUpdateExclusionsInput{
				NextToken: token, MaxResults: sdkaws.Int32(ecrPageLimit),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.PullTimeUpdateExclusions, out.NextToken, nil
		},
		func(repoARN string) (*store.Resource, string, string) {
			if repoARN == "" {
				return nil, "pull time update exclusions are an empty repository ARN", "(an empty exclusion)"
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPullTimeUpdateExclusion, NativeID: repoARN + "/pull-time-update-exclusion",
				Name: &repoARN, Region: &region, AttributesJSON: mustJSON(map[string]string{"EcrRepositoryArn": repoARN}), DiscoveredBy: scanID,
			}, "", ""
		})
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

// scanECRRepositoryCreationTemplates stores the region's repository creation
// templates, those read before a failed page included; a template without a
// Prefix is warned about and not stored.
func scanECRRepositoryCreationTemplates(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "ecr:DescribeRepositoryCreationTemplates",
		func(ctx context.Context, token *string) ([]ecrtypes.RepositoryCreationTemplate, *string, error) {
			out, err := client.DescribeRepositoryCreationTemplates(ctx, &ecr.DescribeRepositoryCreationTemplatesInput{
				NextToken: token, MaxResults: sdkaws.Int32(ecrPageLimit),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RepositoryCreationTemplates, out.NextToken, nil
		},
		func(tmpl ecrtypes.RepositoryCreationTemplate) (*store.Resource, string, string) {
			prefix := sv(tmpl.Prefix)
			if prefix == "" {
				return nil, "repository creation templates have an empty Prefix", cmp.Or(sv(tmpl.Description), "(a template with no Description)")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeECRRepositoryCreationTemplate,
				NativeID: fmt.Sprintf("arn:aws:ecr:%s:%s:repository-creation-template/%s", region, acct.ID, prefix),
				Name:     &prefix, Region: &region, AttributesJSON: mustJSON(tmpl), DiscoveredBy: scanID,
			}, "", ""
		})
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
// Batches are read fanoutMed at a time (readEachKey). Per batch: see
// ecrScanningConfigBatch; an error it returns is returned after the rows of
// every batch, and those it read before the error, are stored. Per
// repository: failure code REPOSITORY_NOT_FOUND (deleted since it
// was listed) is dropped silently. Three other reasons drop a repository's
// row, each with one warning covering every repository it dropped: any other
// failure code, an element without a repositoryName, and an element whose
// repository has no usable stored ARN (none of that name in repos, or one
// that does not parse). Those three warnings are sent only after a
// successful write; a denial is warned about once during the read
// (fanOutErrs.keyErr). A ctx stop is returned as one error.
func scanECRRepositoryScanningConfigs(ctx context.Context, client ecrExtAPI, acct *account, region string, st *store.Store, scanID string, repos []childParent) (int, int, error) {
	const op = "ecr:BatchGetRepositoryScanningConfiguration"
	repoARN := make(map[string]string, len(repos))
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		repoARN[r.id] = r.arn
		names = append(names, r.id)
	}
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: func(error) bool { return false }}
	reads, stopped := readEachKey(ctx, slices.Collect(slices.Chunk(names, ecrScanningConfigBatchMax)),
		func(ctx context.Context, batch []string) ecrScanningConfigRead {
			out, err := ecrScanningConfigBatch(ctx, client, batch, errs)
			return ecrScanningConfigRead{out, err}
		})
	var (
		children                           []containedChild
		batchErrs                          []error
		failedRepos, unnamedEl, unparented skipTally
	)
	interrupted := false
	for _, read := range reads {
		switch {
		case read.err == nil:
		case ctx.Err() != nil && errors.Is(read.err, ctx.Err()):
			interrupted = true
		default:
			batchErrs = append(batchErrs, read.err)
		}
		if read.out == nil {
			continue
		}
		for _, f := range read.out.Failures {
			if f.FailureCode != ecrtypes.ScanningConfigurationFailureCodeRepositoryNotFound {
				failedRepos.skipped(sv(f.RepositoryName), fmt.Errorf("%s: %s", f.FailureCode, sv(f.FailureReason)))
			}
		}
		for _, c := range read.out.ScanningConfigurations {
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
	}
	// One stop member, whether ctx ended before some batches started or only
	// during the batches already running.
	switch {
	case stopped != nil:
		batchErrs = append(batchErrs, fmt.Errorf("%s: %w", op, stopped))
	case interrupted:
		batchErrs = append(batchErrs, fmt.Errorf("%s: reads cut short: %w", op, ctx.Err()))
	}
	total, inserted, err := upsertContained(st, op, children)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, errors.Join(append(batchErrs, err)...)
	}
	failedRepos.warnCode(st, op, acct, region, "a failure code other than REPOSITORY_NOT_FOUND", "repository(ies)",
		"ECR reported a per-repository failure this scanner does not recognise")
	unnamedEl.warnCode(st, op, acct, region, "no repositoryName", "scanning configuration(s)",
		"the row's NativeID is built from it")
	unparented.warnCode(st, op, acct, region, "no usable repository ARN", "scanning configuration(s)",
		"the row's NativeID is built from its repository's ARN")
	return total, inserted, errors.Join(append(batchErrs, err)...)
}

// ecrScanningConfigRead is one batch's result: what it read, and the error
// it ended with.
type ecrScanningConfigRead struct {
	out *ecr.BatchGetRepositoryScanningConfigurationOutput
	err error
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
				return merged, kerr
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

// scanECRPublic adds the public repository phase, then the public registry
// phase, to p under their ops. ecrpublic is global but only callable from
// its home region (us-east-1 for aws; the SDK lists it in no other
// partition), so other regions add nothing and each account's rows are
// stored once. The registry phase runs whatever error the repository phase
// returned, except a failed write; it then links only the repositories stored.
// It reports whether a write failed, after which the scanner must stop.
func scanECRPublic(ctx context.Context, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	if !acct.homeGate(st, "ecrpublic", ecrPublicRepositoriesOp, region) {
		return false
	}
	client := ecrpublic.NewFromConfig(acct.cfg, func(o *ecrpublic.Options) { o.Region = region })
	total, inserted, reposByRegistry, err := scanECRPublicRepositories(ctx, client, acct, region, st, scanID)
	if p.addPhase(ecrPublicRepositoriesOp, total, inserted, err) {
		return true
	}
	total, inserted, err = scanECRPublicRegistries(ctx, client, acct, region, st, scanID, reposByRegistry)
	return p.addPhase(ecrPublicRegistriesOp, total, inserted, err)
}

// scanECRPublicRepositories stores the public repositories, those read before
// a failed page included, and returns their ARNs keyed by RegistryId, for the
// registry phase's contains edges. A repository without an ARN is warned
// about and not stored.
func scanECRPublicRepositories(ctx context.Context, client ecrPublicAPI, acct *account, region string, st *store.Store, scanID string) (int, int, map[string][]string, error) {
	reposByRegistry := map[string][]string{}
	total, inserted, err := storeTokenList(ctx, st, acct, region, ecrPublicRepositoriesOp,
		func(ctx context.Context, token *string) ([]ecrpublictypes.Repository, *string, error) {
			out, err := client.DescribeRepositories(ctx, &ecrpublic.DescribeRepositoriesInput{
				NextToken: token, MaxResults: sdkaws.Int32(ecrPageLimit),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Repositories, out.NextToken, nil
		},
		func(r ecrpublictypes.Repository) (*store.Resource, string, string) {
			arn := sv(r.RepositoryArn)
			if arn == "" {
				return nil, "public repositories have an empty RepositoryArn", cmp.Or(sv(r.RepositoryName), "(a repository with no RepositoryName)")
			}
			label := sv(r.RepositoryName)
			if label == "" {
				label = arn
			}
			if regID := sv(r.RegistryId); regID != "" {
				reposByRegistry[regID] = append(reposByRegistry[regID], arn)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPublicRepository, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, "", ""
		})
	return total, inserted, reposByRegistry, err
}

const (
	ecrPublicRepositoriesOp = "ecr-public:DescribeRepositories"
	ecrPublicRegistriesOp   = "ecr-public:DescribeRegistries"
)

// scanECRPublicRegistries stores the account's public registry and links it to
// the repositories the repository phase stored under the same RegistryId. A
// failed page keeps the registries read before it; a registry without an ARN
// is warned about and not stored.
func scanECRPublicRegistries(ctx context.Context, client ecrPublicAPI, acct *account, region string, st *store.Store, scanID string, reposByRegistry map[string][]string) (int, int, error) {
	var pairs [][2]string
	total, inserted, err := storeTokenList(ctx, st, acct, region, ecrPublicRegistriesOp,
		func(ctx context.Context, token *string) ([]ecrpublictypes.Registry, *string, error) {
			out, err := client.DescribeRegistries(ctx, &ecrpublic.DescribeRegistriesInput{
				NextToken: token, MaxResults: sdkaws.Int32(ecrPageLimit),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Registries, out.NextToken, nil
		},
		func(r ecrpublictypes.Registry) (*store.Resource, string, string) {
			arn := sv(r.RegistryArn)
			if arn == "" {
				return nil, "public registries have an empty RegistryArn", cmp.Or(sv(r.RegistryId), "(a registry with no RegistryId)")
			}
			parentID := store.ResourceID("aws", acct.ID, arn)
			for _, repoARN := range reposByRegistry[sv(r.RegistryId)] {
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, repoARN), parentID})
			}
			label := ecrPublicRegistryLabel(r)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECRPublicRegistry, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, "", ""
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if herr := st.RecordHierarchyBatch(pairs); herr != nil {
		return total, inserted, errors.Join(err, fmt.Errorf("closure ecr public-registries: %w", herr))
	}
	return total, inserted, err
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
