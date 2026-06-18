package aws

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/viper"
)

// emulatorAccountIDOverride returns the explicit account_id the caller
// (typically an external orchestrator) wants recorded on resources/
// scans, gated on AWS_ENDPOINT_URL being set. The gate is the AWS SDK's
// canonical "talking to a non-AWS endpoint" signal — prod scanners
// never set it, so the override is unreachable in prod. Emulators
// (e.g. LocalStack) return a sentinel "000000000000" from
// sts:GetCallerIdentity that would otherwise overwrite the configured
// account id on every scan. Returns the empty string when either env is
// unset or whitespace-only.
func emulatorAccountIDOverride() string {
	if strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL")) == "" {
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
// assume-role target; role_chain (if non-empty) takes precedence and walks
// the slice in order, each step using the prior step's credentials as the
// source. Use role_chain when a hub/spoke topology requires hopping through
// an intermediate "audit" role to reach a target account.
type accountCfg struct {
	ID        string   `mapstructure:"id"`
	Name      string   `mapstructure:"name"`
	Regions   []string `mapstructure:"regions"`
	RoleARN   string   `mapstructure:"role_arn"`
	RoleChain []string `mapstructure:"role_chain"`
}

// loadAccounts parses the viper config and returns a resolved account slice.
// When no accounts are configured, the current account is detected via STS.
// profile selects a named entry from ~/.aws/config ("" = default chain).
// regionOverride, when non-empty, replaces all per-account and default regions.
//
// When roleARNOverride is non-empty, the config-file accounts: section is
// ignored entirely: a single synthetic account is built that assumes
// roleARNOverride (with externalIDOverride passed as the STS ExternalId
// when non-empty). An external orchestrator (e.g. a scan-trigger Lambda)
// uses this to drive per-tenant scans without writing config to disk in the
// worker container.
func loadAccounts(ctx context.Context, profile string, regionOverride []string, roleARNOverride, externalIDOverride string) ([]account, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("aws", &cfg); err != nil {
		return nil, fmt.Errorf("parse aws config: %w", err)
	}
	if len(cfg.DefaultRegions) == 0 {
		cfg.DefaultRegions = []string{"us-east-1"}
	}

	// Build SDK load options; optionally select a named credential profile.
	// Adaptive retry mode uses a client-side token bucket that learns from
	// throttling responses and proactively slows down requests. 10 max attempts
	// gives the backoff enough headroom for low-rate-limit services like IAM.
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithRetryMaxAttempts(10),
		awsconfig.WithRetryMode(sdkaws.RetryModeAdaptive),
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	// Load the base SDK config once (uses default credential chain: env → ~/.aws → IAM role).
	baseCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws sdk config: %w", err)
	}

	// CLI/Lambda override pins a single AssumeRole-driven account; ignore
	// config-file accounts entirely. Build the synthetic accountCfg here so
	// the loop below handles role chain + region + override uniformly.
	if roleARNOverride != "" {
		stsClient := sts.NewFromConfig(baseCfg)
		acctCfg := baseCfg
		acctCfg.Credentials = cachedAssumeRole(stsClient, roleARNOverride, func(o *stscreds.AssumeRoleOptions) {
			if externalIDOverride != "" {
				o.ExternalID = &externalIDOverride
			}
		})
		// Resolve the assumed-role caller account for the synthetic ID; on
		// error we still proceed with an empty ID rather than failing the
		// scan, since the override path is meant to short-circuit STS pre-checks.
		var acctID string
		if envAcctID := emulatorAccountIDOverride(); envAcctID != "" {
			acctID = envAcctID
		} else if identity, err := sts.NewFromConfig(acctCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err == nil {
			acctID = sv(identity.Account)
		}
		regions := cfg.DefaultRegions
		if len(regionOverride) > 0 {
			regions = regionOverride
		}
		return []account{{
			ID:      acctID,
			Regions: regions,
			cfg:     acctCfg,
		}}, nil
	}

	// Auto-detect the current account when none are configured.
	// Emulator override (F28) short-circuits STS when AWS_ENDPOINT_URL
	// is set, so emulator-backed scans record the configured account id
	// instead of the emulator's sentinel "000000000000".
	if len(cfg.Accounts) == 0 {
		if envAcctID := emulatorAccountIDOverride(); envAcctID != "" {
			cfg.Accounts = []accountCfg{{ID: envAcctID}}
		} else {
			stsClient := sts.NewFromConfig(baseCfg)
			identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
			if err != nil {
				return nil, fmt.Errorf("sts:GetCallerIdentity: %w", err)
			}
			cfg.Accounts = []accountCfg{{ID: sv(identity.Account)}}
		}
	}

	accounts := make([]account, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		acctCfg := baseCfg // value copy; safe — aws.Config is a struct

		// Multi-hop role chaining (preferred when present): walk role_chain
		// in order, each step's STS client built from the prior step's
		// credentials. Each AssumeRoleProvider is wrapped in a
		// CredentialsCache so the SDK's normal token refresh kicks in
		// independently per hop.
		switch {
		case len(a.RoleChain) > 0:
			acctCfg.Credentials = chainAssumeRoles(acctCfg, a.RoleChain)
		case a.RoleARN != "":
			acctCfg.Credentials = cachedAssumeRole(sts.NewFromConfig(baseCfg), a.RoleARN, nil)
		}

		regions := a.Regions
		if len(regions) == 0 {
			// No per-account regions configured: use the default region list.
			// To scan all opted-in regions, list them explicitly under accounts[].regions
			// or set aws.default_regions in config.
			regions = cfg.DefaultRegions
		}
		// CLI --region flag overrides both per-account and default regions.
		if len(regionOverride) > 0 {
			regions = regionOverride
		}

		accounts = append(accounts, account{
			ID:      a.ID,
			Name:    a.Name,
			Regions: regions,
			cfg:     acctCfg,
		})
	}
	return accounts, nil
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
func chainAssumeRoles(baseCfg sdkaws.Config, roleARNs []string) *sdkaws.CredentialsCache {
	cur := baseCfg
	var last *sdkaws.CredentialsCache
	for _, role := range roleARNs {
		cache := cachedAssumeRole(sts.NewFromConfig(cur), role, nil)
		next := cur
		next.Credentials = cache
		cur = next
		last = cache
	}
	return last
}

const (
	// assumeRoleSessionDuration is the lifetime requested for each assumed-role
	// session. 1h is the hard cap AWS enforces on chained AssumeRole (>3600s is
	// rejected) and sits within every role's MaxSessionDuration (the minimum is
	// 1h), so it is universally safe for both single-hop and chained paths.
	assumeRoleSessionDuration = time.Hour
	// credRefreshWindow makes the CredentialsCache refresh this long BEFORE the
	// session actually expires, so a long scan never races an expiring token.
	// Jitter spreads refreshes across the concurrent service goroutines.
	credRefreshWindow = 5 * time.Minute
)

// cachedAssumeRole builds a CredentialsCache around an AssumeRoleProvider with a
// proactive refresh window, so the SDK renews the session before it expires
// rather than lazily at the expiry boundary. extra, when non-nil, customizes the
// AssumeRole options (e.g. ExternalID); Duration is always set first.
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
