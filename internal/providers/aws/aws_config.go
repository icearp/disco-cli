package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
	"github.com/spf13/viper"
)

// emulatorMode reports whether AWS_ENDPOINT_URL is set — the SDK's canonical
// "talking to a non-AWS endpoint" signal. A scan against real AWS would
// normally leave it unset.
func emulatorMode() bool {
	return strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL")) != ""
}

// emulatorAccountIDOverride returns the explicit account_id the caller
// (typically an external orchestrator) wants recorded on resources/scans,
// gated on emulatorMode, so it is inert unless AWS_ENDPOINT_URL is set. An
// emulator can answer sts:GetCallerIdentity with a sentinel account
// (LocalStack returns "000000000000") that would otherwise overwrite the
// configured account id on every scan. Returns "" when either env is unset
// or whitespace-only.
func emulatorAccountIDOverride() string {
	if !emulatorMode() {
		return ""
	}
	return strings.TrimSpace(os.Getenv("DISCO_CLOUD_ACCOUNT_ID"))
}

// providerCfg mirrors the aws: section of ~/.disco/config.yaml.
type providerCfg struct {
	DefaultRegions []string     `mapstructure:"default_regions"`
	Accounts       []accountCfg `mapstructure:"accounts"`
}

// accountCfg is the per-account YAML entry. role_arn is the single-hop
// assume-role target; role_chain, if non-empty, takes precedence and walks
// the slice in order, each step using the prior step's credentials as the
// source. Use role_chain for hub/spoke topologies that hop through an
// intermediate "audit" role to reach a target account.
//
// partition, when set, must agree with the partition the role ARN and regions
// name; when unset it is derived from them (resolvePartition), except in
// emulator mode, where a partition other than aws must be set. profile names
// the shared-config profile the account's base credentials come from; unset,
// the account uses --profile or the default chain.
type accountCfg struct {
	ID        string   `mapstructure:"id"`
	Name      string   `mapstructure:"name"`
	Regions   []string `mapstructure:"regions"`
	RoleARN   string   `mapstructure:"role_arn"`
	RoleChain []string `mapstructure:"role_chain"`
	Partition string   `mapstructure:"partition"`
	Profile   string   `mapstructure:"profile"`
}

// roleTarget returns the ARN of the role the account's credentials end in,
// and which setting it came from; role_chain wins over role_arn, as it does
// when the credentials are built.
func (a accountCfg) roleTarget() (string, partitionSource) {
	if len(a.RoleChain) > 0 {
		return a.RoleChain[len(a.RoleChain)-1], sourceRoleChain
	}
	return a.RoleARN, sourceRoleARN
}

// baseLoadOptions returns the SDK load options every account config derives
// from; profile, when non-empty, selects a named credential profile.
// Adaptive retry mode uses a client-side token bucket that learns from
// throttling responses and slows requests proactively. 10 max attempts gives
// backoff enough headroom for low-rate-limit services like IAM. The API option
// gives every client the error-message repair the message-based predicates in
// aws_errors.go depend on (errorMessageKeyRepair).
func baseLoadOptions(profile string) []func(*awsconfig.LoadOptions) error {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRetryMaxAttempts(10),
		awsconfig.WithRetryMode(sdkaws.RetryModeAdaptive),
		awsconfig.WithAPIOptions([]func(*middleware.Stack) error{normalizeErrorMessageKey}),
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	return opts
}

// baseConfigLoader loads the SDK config an account's credentials derive from;
// profile "" means the default credential chain. The config's Region is what
// the profile or environment set, "" when neither did. loadAccounts takes it
// as a parameter so tests can substitute a stubbed config.
type baseConfigLoader func(ctx context.Context, profile string) (sdkaws.Config, error)

// loadBaseConfig is the production baseConfigLoader: the default credential
// chain (env → ~/.aws → IAM role), or the named profile.
func loadBaseConfig(ctx context.Context, profile string) (sdkaws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, baseLoadOptions(profile)...)
	if err != nil {
		return sdkaws.Config{}, explainConfigLoadError(err, profile)
	}
	return cfg, nil
}

// baseConfig is a loaded base config plus the region its profile or
// environment named, kept because cfg.Region may hold the fallback instead.
type baseConfig struct {
	cfg           sdkaws.Config
	profileRegion string // "" when the profile and environment name no region
}

// loadBaseFor loads profile's base config and fills in the fallback region.
func loadBaseFor(ctx context.Context, loadBase baseConfigLoader, profile string) (baseConfig, error) {
	cfg, err := loadBase(ctx, profile)
	if err != nil {
		return baseConfig{}, err
	}
	b := baseConfig{cfg: cfg, profileRegion: cfg.Region}
	// Honor the region from profile/env; fall back to us-east-1 only when none
	// is configured, since global-endpoint clients built from this config need
	// a non-empty region. (The us-east-1-pinned probes — enabledScanRegions,
	// region-availability SSM — set o.Region themselves, so unaffected.) The
	// fallback is not partition evidence, so profileRegion keeps the "".
	if b.cfg.Region == "" {
		b.cfg.Region = "us-east-1"
	}
	return b, nil
}

// implicitDefaultRegions is the region list an account scans when neither the
// account, aws.default_regions nor --regions names one.
var implicitDefaultRegions = []string{"us-east-1"}

// loadAccounts parses the viper config and returns a resolved account slice.
// When no accounts are configured, the current account is detected via STS.
// profile selects a named entry from ~/.aws/config ("" = default chain) for
// every account that sets no profile of its own. regionOverride, when
// non-empty, replaces all per-account and default regions.
//
// When roleARNOverride is non-empty, the config-file accounts: section is
// ignored entirely: a single synthetic account is built that assumes
// roleARNOverride (externalIDOverride passed as the STS ExternalId when
// non-empty). An external orchestrator (e.g. a scan-trigger Lambda) uses
// this to drive per-tenant scans without writing config to disk in the
// worker container.
func loadAccounts(ctx context.Context, loadBase baseConfigLoader, profile string, regionOverride []string, roleARNOverride, externalIDOverride, sourceIdentity string) ([]account, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("aws", &cfg); err != nil {
		return nil, fmt.Errorf("parse aws config: %w", err)
	}

	if roleARNOverride != "" {
		base, err := loadBaseFor(ctx, loadBase, profile)
		if err != nil {
			return nil, err
		}
		configured := firstNonEmpty(regionOverride, cfg.DefaultRegions)
		return loadOverrideAccount(ctx, base, roleARNOverride, assumeRoleOpts(externalIDOverride, sourceIdentity), configured)
	}

	bases := map[string]baseConfig{}
	baseFor := func(profile string) (baseConfig, error) {
		if b, ok := bases[profile]; ok {
			return b, nil
		}
		b, err := loadBaseFor(ctx, loadBase, profile)
		if err != nil {
			return baseConfig{}, err
		}
		bases[profile] = b
		return b, nil
	}

	type pendingAccount struct {
		cfg       accountCfg
		callerARN string
	}
	pending := make([]pendingAccount, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		pending = append(pending, pendingAccount{cfg: a})
	}
	// Auto-detect the current account when none are configured. Emulator
	// override (F28) short-circuits STS when AWS_ENDPOINT_URL is set, so
	// emulator-backed scans record the configured account id instead of the
	// emulator's sentinel "000000000000".
	if len(pending) == 0 {
		if envAcctID := emulatorAccountIDOverride(); envAcctID != "" {
			pending = append(pending, pendingAccount{cfg: accountCfg{ID: envAcctID}})
		} else {
			base, err := baseFor(profile)
			if err != nil {
				return nil, err
			}
			identity, err := sts.NewFromConfig(base.cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
			if err != nil {
				return nil, fmt.Errorf("sts:GetCallerIdentity: %w", err)
			}
			pending = append(pending, pendingAccount{cfg: accountCfg{ID: sv(identity.Account)}, callerARN: sv(identity.Arn)})
		}
	}

	type claim struct{ account, partition string }
	claimed := map[string]claim{} // effective profile → first account resolved with it
	accounts := make([]account, 0, len(pending))
	for _, p := range pending {
		a := p.cfg
		acctProfile := cmp.Or(a.Profile, profile)
		base, err := baseFor(acctProfile)
		if err != nil {
			return nil, err
		}
		acctCfg := base.cfg // value copy; safe — aws.Config is a struct

		// Multi-hop role chaining (preferred when present): walk role_chain
		// in order, each step's STS client built from the prior step's
		// credentials. Each AssumeRoleProvider wraps in a CredentialsCache so
		// the SDK's normal token refresh kicks in independently per hop.
		roleARN, roleSource := a.roleTarget()
		switch {
		case roleSource == sourceRoleChain:
			acctCfg.Credentials = chainAssumeRoles(acctCfg, a.RoleChain, sourceIdentity)
		case roleARN != "":
			acctCfg.Credentials = cachedAssumeRole(sts.NewFromConfig(base.cfg), roleARN, assumeRoleOpts("", sourceIdentity))
		}

		// The CLI --regions override wins over per-account regions, which win
		// over aws.default_regions. To scan all opted-in regions, list "all"
		// in any of them.
		configured := firstNonEmpty(regionOverride, a.Regions, cfg.DefaultRegions)

		label := cmp.Or(a.ID, a.Name)
		partition, err := resolvePartition(label, partitionInputs{
			explicit:      a.Partition,
			roleARN:       roleARN,
			roleSource:    roleSource,
			callerARN:     p.callerARN,
			regions:       configured,
			profileRegion: base.profileRegion,
		})
		if err != nil {
			return nil, err
		}
		if first, ok := claimed[acctProfile]; !ok {
			claimed[acctProfile] = claim{account: label, partition: partition}
		} else if first.partition != partition {
			return nil, &sharedCredentialsError{
				Profile:      acctProfile,
				FirstAccount: first.account, FirstPartition: first.partition,
				SecondAccount: label, SecondPartition: partition,
			}
		}

		accounts = append(accounts, account{
			ID:        a.ID,
			Name:      a.Name,
			Regions:   expandAllRegions(firstNonEmpty(configured, implicitDefaultRegions)),
			Partition: partition,
			cfg:       acctCfg,
		})
	}
	return accounts, nil
}

// loadOverrideAccount builds the single account the --role-arn override pins
// a scan to. configured is the --regions or aws.default_regions list, empty
// when neither is set.
func loadOverrideAccount(ctx context.Context, base baseConfig, roleARN string, assumeOpts func(*stscreds.AssumeRoleOptions), configured []string) ([]account, error) {
	acctCfg := base.cfg
	acctCfg.Credentials = cachedAssumeRole(sts.NewFromConfig(base.cfg), roleARN, assumeOpts)
	// Resolve the assumed-role caller account for the synthetic ID; on
	// error, proceed with an empty ID rather than failing the scan — the
	// override path is meant to short-circuit STS pre-checks.
	var acctID, callerARN string
	if envAcctID := emulatorAccountIDOverride(); envAcctID != "" {
		acctID = envAcctID
	} else if identity, err := sts.NewFromConfig(acctCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err == nil {
		acctID = sv(identity.Account)
		callerARN = sv(identity.Arn)
	}
	partition, err := resolvePartition(cmp.Or(acctID, roleARN), partitionInputs{
		roleARN:       roleARN,
		roleSource:    sourceRoleOverride,
		callerARN:     callerARN,
		regions:       configured,
		profileRegion: base.profileRegion,
	})
	if err != nil {
		return nil, err
	}
	return []account{{
		ID:        acctID,
		Regions:   expandAllRegions(firstNonEmpty(configured, implicitDefaultRegions)),
		Partition: partition,
		cfg:       acctCfg,
	}}, nil
}

// firstNonEmpty returns the first non-empty list, or nil when all are empty.
func firstNonEmpty(lists ...[]string) []string {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}

// explainConfigLoadError augments aws-sdk-go-v2's opaque assume-role failure
// ("… of profile <src>, <nil>") with an actionable hint. The nil inner error
// means the source profile has no SDK-resolvable credentials (no static keys,
// credential_process, or sso_session) — often because the AWS CLI resolves
// them via a custom credential helper the Go SDK can't invoke. Exporting the
// CLI-resolved creds sidesteps it.
//
// A known trigger is an `aws login` source profile (login_session): the SDK rejects
// role_arn + source_profile=<login-only> at config-load time because hasCredentials()
// omits LoginSession, even though it resolves login_session fine standalone. Upstream
// bug — see aws-sdk-go-v2#3446 / PR #3447; drop this once that fix ships and we bump the SDK.
func explainConfigLoadError(err error, profile string) error {
	var arErr awsconfig.SharedConfigAssumeRoleError
	if !errors.As(err, &arErr) {
		return fmt.Errorf("load aws sdk config: %w", err)
	}
	target := profile // the profile the user actually invoked
	if target == "" {
		target = arErr.Profile
	}
	return fmt.Errorf("load aws sdk config: cannot assume role %s: source profile %q has no "+
		"SDK-resolvable credentials (needs static keys, credential_process, or sso_session). "+
		"If your AWS CLI uses a custom credential helper, export the resolved credentials and "+
		"re-run without --profile:\n  eval \"$(aws configure export-credentials --profile %s --format env)\"\n: %w",
		arErr.RoleARN, arErr.Profile, target, err)
}

// chainAssumeRoles walks roleARNs in order, building a credentials provider
// where each hop's STS client uses the prior hop's credentials. Returns a
// cached provider suitable for sdkaws.Config.Credentials. The first hop uses
// baseCfg's credentials (env / shared / IRSA / etc.); each subsequent hop
// inherits the previous AssumeRoleProvider's caller identity.
//
// Use cases: hub-and-spoke org topology where the runner credentials live in
// a security account, jumping through a per-org "Audit" role into each
// member account, then optionally into a per-account read-only role.
func chainAssumeRoles(baseCfg sdkaws.Config, roleARNs []string, sourceIdentity string) *sdkaws.CredentialsCache {
	cur := baseCfg
	var last *sdkaws.CredentialsCache
	for i, role := range roleARNs {
		// SourceIdentity is set on the entry hop only — AWS auto-propagates it
		// to every downstream session in the chain. Re-asserting it on later
		// hops would force each role's trust policy to grant
		// sts:SetSourceIdentity; setting it once means only the entry role
		// needs that permission.
		var extra func(*stscreds.AssumeRoleOptions)
		if i == 0 {
			extra = assumeRoleOpts("", sourceIdentity)
		}
		cache := cachedAssumeRole(sts.NewFromConfig(cur), role, extra)
		next := cur
		next.Credentials = cache
		cur = next
		last = cache
	}
	return last
}

const (
	// assumeRoleSessionDuration is the lifetime requested for each assumed-role
	// session. 1h is AWS's hard cap on chained AssumeRole (>3600s rejected)
	// and sits within every role's MaxSessionDuration (minimum 1h), so it's
	// safe for both single-hop and chained paths.
	assumeRoleSessionDuration = time.Hour
	// credRefreshWindow makes the CredentialsCache refresh this long BEFORE
	// the session actually expires, so a long scan never races an expiring
	// token. Jitter spreads refreshes across concurrent service goroutines.
	credRefreshWindow = 5 * time.Minute
)

// cachedAssumeRole builds a CredentialsCache around an AssumeRoleProvider
// with a proactive refresh window, so the SDK renews the session before it
// expires rather than lazily at the expiry boundary. extra, when non-nil,
// customizes the AssumeRole options (e.g. ExternalID); Duration is always
// set first.
func cachedAssumeRole(client *sts.Client, roleARN string, extra func(*stscreds.AssumeRoleOptions)) *sdkaws.CredentialsCache {
	provider := stscreds.NewAssumeRoleProvider(client, roleARN, func(o *stscreds.AssumeRoleOptions) {
		o.Duration = assumeRoleSessionDuration
		if extra != nil {
			extra(o)
		}
	})
	return sdkaws.NewCredentialsCache(provider, func(o *sdkaws.CredentialsCacheOptions) {
		o.ExpiryWindow = credRefreshWindow
		o.ExpiryWindowJitterFrac = 0.5
	})
}

// assumeRoleOpts builds the AssumeRole option closure shared by every assume
// site. Returns nil when neither value is set, so callers pass a no-op extra
// without an empty closure. SourceIdentity, once set on the entry session, is
// stamped on every API call's CloudTrail record and propagated through role
// chains — but the target role's trust policy must grant
// sts:SetSourceIdentity or the assume fails, which is why it's opt-in.
func assumeRoleOpts(externalID, sourceIdentity string) func(*stscreds.AssumeRoleOptions) {
	if externalID == "" && sourceIdentity == "" {
		return nil
	}
	return func(o *stscreds.AssumeRoleOptions) {
		if externalID != "" {
			o.ExternalID = &externalID
		}
		if sourceIdentity != "" {
			o.SourceIdentity = &sourceIdentity
		}
	}
}

// sourceIdentityAuto is the reserved --source-identity token that resolves to the
// disco scan ID, so CloudTrail's sourceIdentity maps back to the scans table row.
const sourceIdentityAuto = "auto"

// resolveSourceIdentity turns the configured --source-identity value into the
// literal stamped on the session: "" stays off, the reserved "auto" token
// becomes the scan ID, anything else is used verbatim.
func resolveSourceIdentity(configured, scanID string) string {
	switch configured {
	case "":
		return ""
	case sourceIdentityAuto:
		return scanID
	default:
		return configured
	}
}

// sourceIdentityPattern is AWS's SourceIdentity constraint: 2–64 chars from the
// set [\w+=,.@-]. The 32-hex scan ID always satisfies it.
var sourceIdentityPattern = regexp.MustCompile(`^[\w+=,.@-]{2,64}$`)

func validateSourceIdentity(s string) error {
	if !sourceIdentityPattern.MatchString(s) {
		return fmt.Errorf("%q must be 2-64 chars from [A-Za-z0-9_+=,.@-]", s)
	}
	return nil
}

// describeRegionsAPI is the test seam for region-enablement probing.
// *ec2.Client satisfies it; tests inject a stub.
type describeRegionsAPI interface {
	DescribeRegions(context.Context, *ec2.DescribeRegionsInput, ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

// enabledRegionSet returns the regions enabled for the calling account
// (opt-in-not-required or opted-in). Calling a regional endpoint for a
// not-opted-in region returns AuthFailure / UnrecognizedClientException, so
// filtering the scan to this set avoids a storm of doomed, retried calls.
// Mirrors the opt-in-status filter in aws_coverage.go::FetchRegions.
func enabledRegionSet(ctx context.Context, c describeRegionsAPI) (map[string]bool, error) {
	allRegions := true
	out, err := c.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
		Filters: []ec2types.Filter{{
			Name:   sp("opt-in-status"),
			Values: []string{"opt-in-not-required", "opted-in"},
		}},
	})
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(out.Regions))
	for _, r := range out.Regions {
		if r.RegionName != nil {
			set[*r.RegionName] = true
		}
	}
	return set, nil
}

// filterToEnabled partitions requested into the regions present in enabled
// (kept) and those absent (skipped), preserving input order.
func filterToEnabled(requested []string, enabled map[string]bool) (kept, skipped []string) {
	for _, r := range requested {
		if enabled[r] {
			kept = append(kept, r)
		} else {
			skipped = append(skipped, r)
		}
	}
	return kept, skipped
}
