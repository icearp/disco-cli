package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/viper"
)

// providerCfg mirrors the aws: section of ~/.disco/config.yaml.
type providerCfg struct {
	DefaultRegions []string     `mapstructure:"default_regions"`
	Accounts       []accountCfg `mapstructure:"accounts"`
}

// accountCfg is the per-account YAML entry.
type accountCfg struct {
	ID      string   `mapstructure:"id"`
	Name    string   `mapstructure:"name"`
	Regions []string `mapstructure:"regions"`
	RoleARN string   `mapstructure:"role_arn"`
}

// loadAccounts parses the viper config and returns a resolved account slice.
// When no accounts are configured, the current account is detected via STS.
// profile selects a named entry from ~/.aws/config ("" = default chain).
// regionOverride, when non-empty, replaces all per-account and default regions.
func loadAccounts(ctx context.Context, profile string, regionOverride []string) ([]account, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("aws", &cfg); err != nil {
		return nil, fmt.Errorf("parse aws config: %w", err)
	}
	if len(cfg.DefaultRegions) == 0 {
		cfg.DefaultRegions = []string{"us-east-1"}
	}

	// Build SDK load options; optionally select a named credential profile.
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion("us-east-1"),
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	// Load the base SDK config once (uses default credential chain: env → ~/.aws → IAM role).
	baseCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws sdk config: %w", err)
	}

	// Auto-detect the current account when none are configured.
	if len(cfg.Accounts) == 0 {
		stsClient := sts.NewFromConfig(baseCfg)
		identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return nil, fmt.Errorf("sts:GetCallerIdentity: %w", err)
		}
		cfg.Accounts = []accountCfg{{ID: sv(identity.Account)}}
	}

	accounts := make([]account, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		acctCfg := baseCfg // value copy; safe — aws.Config is a struct

		// Assume a cross-account role when role_arn is configured.
		if a.RoleARN != "" {
			stsClient := sts.NewFromConfig(baseCfg)
			provider := stscreds.NewAssumeRoleProvider(stsClient, a.RoleARN)
			acctCfg.Credentials = sdkaws.NewCredentialsCache(provider)
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
