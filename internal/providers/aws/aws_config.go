package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
func loadAccounts(ctx context.Context) ([]account, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("aws", &cfg); err != nil {
		return nil, fmt.Errorf("parse aws config: %w", err)
	}
	if len(cfg.DefaultRegions) == 0 {
		cfg.DefaultRegions = []string{"us-east-1"}
	}

	// Load the base SDK config once (uses default credential chain: env → ~/.aws → IAM role).
	baseCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
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

		accounts = append(accounts, account{
			ID:      a.ID,
			Name:    a.Name,
			Regions: regions,
			cfg:     acctCfg,
		})
	}
	return accounts, nil
}

// enabledRegions calls EC2 DescribeRegions (opt-in-status=opted-in) to return
// the regions enabled for this account.
func enabledRegions(ctx context.Context, cfg sdkaws.Config) ([]string, error) {
	client := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.Region = "us-east-1" })
	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: sdkaws.Bool(false), // only opted-in regions
	})
	if err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(out.Regions))
	for _, r := range out.Regions {
		if r.RegionName != nil {
			regions = append(regions, *r.RegionName)
		}
	}
	return regions, nil
}
