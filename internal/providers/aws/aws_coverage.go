package aws

import (
	"context"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for AWS: the types its
// scanners emit (CollectEmits) and, for --cross-check, the live registry
// (CloudFormation ListTypes unioned with the credential-free Service
// Reference catalog, see CrossCheck).
type coverageProvider struct{}

func (coverageProvider) Name() string { return "aws" }

// Emits returns CollectEmits() verbatim — the edge-less flag on each TypeDecl is
// set at registration time alongside the scanner's emits decl, keeping the
// decision next to the SDK-shape author who knows whether the type carries
// outbound refs.
func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// TypeServices implements coverage.ServiceMapper from the registration files.
func (coverageProvider) TypeServices() map[string][]string { return typeOrigin.TypeServices() }

// ListResolvers implements coverage.ResolverAuditor by adapting the package's
// ListResolvers() registry view into the neutral coverage shape, so cmd can
// render `disco coverage resolvers` without importing this package directly.
func (coverageProvider) ListResolvers() []coverage.ResolverInfo {
	src := ListResolvers()
	out := make([]coverage.ResolverInfo, len(src))
	for i, r := range src {
		out[i] = coverage.ResolverInfo{Name: r.Name, EdgeCount: r.EdgeCount, Services: r.Services}
	}
	return out
}

// ResolverEdgeSources implements coverage.ResolverAuditor: the distinct
// EdgeDecl.Source disco-types declared across every registered resolver.
func (coverageProvider) ResolverEdgeSources() []string {
	edges := CollectResolverEdges()
	seen := make(map[string]struct{}, len(edges))
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		if _, dup := seen[e.Source]; dup {
			continue
		}
		seen[e.Source] = struct{}{}
		out = append(out, e.Source)
	}
	return out
}

// canonService normalizes a service segment: lowercase, separators
// stripped. Services are NOT de-pluralized — many legitimately end in "s"
// (aidevops, logs, ecs, sns) and CFN/SR agree on the plural.
func canonService(s string) string { return sdkinv.Canon(s) }

// canonResource normalizes a resource segment to its equality stem and drops
// the Service-Reference "Resource" suffix when a non-empty stem remains (so
// "Resources" stays "resource", never collapses to ""). The stem is an
// internal matching identity, never displayed.
// The suffix is trimmed on the raw name, before Ident, because Ident ends in
// a trailing e/s strip: trimming after it left "ThemeResource" as "theme"
// while the candidate leaf "theme" reduced to "them", and the two never met.
func canonResource(s string) string {
	if stem := strings.TrimSuffix(s, "Resource"); stem != "" && stem != s {
		s = stem
	}
	s = sdkinv.Ident(s)
	if stem := strings.TrimSuffix(s, "resourc"); stem != "" && stem != s {
		s = stem
	}
	return s
}

// CanonicalKey normalizes an "AWS::svc::res" upstream key to a catalog-agnostic
// identity so a CloudFormation spelling and its Service-Reference twin collapse
// to one resource (e.g. AWS::Amplify::App and AWS::amplify::apps both →
// "amplify::app"). `coverage services --cross-check` compares it with
// RegistryKey so the SR/CFN twins collapse onto one candidate.
func (coverageProvider) CanonicalKey(upstreamKey string) string {
	parts := strings.SplitN(upstreamKey, "::", 3)
	if len(parts) != 3 {
		return strings.ToLower(upstreamKey)
	}
	return canonService(parts[1]) + "::" + canonResource(parts[2])
}

// RegistryKey is the candidate's identity in CanonicalKey's namespace, so a
// cross-check compares "ec2/instance" with CFN "AWS::EC2::Instance" and SR
// "AWS::ec2::instance" alike.
func (coverageProvider) RegistryKey(c sdkinv.Candidate) string {
	return canonService(c.Service) + "::" + canonResource(c.Key[strings.LastIndex(c.Key, "/")+1:])
}

// CrossCheck returns the union of CloudFormation ListTypes (Public, Resource)
// and the AWS Service Reference catalog. CFN supplies registry-modeled
// resources; Service Reference supplies the SDK-real resources CFN omits.
// Third-party CFN types (community / Hooks / Modules) are filtered out.
func (coverageProvider) CrossCheck(ctx context.Context, opts coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	regions := opts.Regions
	if len(regions) == 0 {
		regions = []string{"us-east-1"}
	}
	seen := map[string]struct{}{}
	var out []coverage.UpstreamType
	for _, region := range regions {
		cfgOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
		if opts.Profile != "" {
			cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
		if err != nil {
			return nil, fmt.Errorf("load aws config (%s): %w", region, err)
		}
		client := cloudformation.NewFromConfig(cfg)

		input := &cloudformation.ListTypesInput{
			Visibility: cftypes.VisibilityPublic,
			Type:       cftypes.RegistryTypeResource,
		}
		paginator := cloudformation.NewListTypesPaginator(client, input)
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("cfn ListTypes (%s): %w", region, err)
			}
			for _, s := range page.TypeSummaries {
				if s.TypeName == nil {
					continue
				}
				name := *s.TypeName
				// Filter to AWS-vendor types only; third-party + Hooks /
				// Modules carry different prefixes.
				if !strings.HasPrefix(name, "AWS::") {
					continue
				}
				if _, dup := seen[name]; dup {
					continue
				}
				seen[name] = struct{}{}
				parts := strings.SplitN(name, "::", 3)
				svc := ""
				if len(parts) == 3 {
					svc = parts[1]
				}
				out = append(out, coverage.UpstreamType{Key: name, Service: svc})
			}
		}
	}

	// Union the credential-free AWS Service Reference catalog. CFN's registry
	// only lists resources with a CloudFormation provider; Service Reference
	// supplies the SDK-real resources CFN omits (DynamoDB streams, AuditManager
	// controls, IdentityStore users, Macie classification jobs). Both fetches
	// are fatal on failure — the union requires both, so a partial fetch can't
	// silently report registry drift. CrossCheck dedupes by canonical identity.
	srTypes, err := fetchServiceReference(ctx)
	if err != nil {
		return nil, fmt.Errorf("service reference fetch: %w", err)
	}
	out = append(out, srTypes...)
	return out, nil
}

// FetchRegions calls ec2:DescribeRegions(AllRegions=true) and returns the
// authoritative AWS region-name list, filtered to commercial-partition
// regions the caller can opt into. Excludes regions the account hasn't
// opted into (Status != "opt-in-not-required" && != "opted-in") so they
// don't masquerade as missing in `disco coverage --regions`.
func (coverageProvider) FetchRegions(ctx context.Context, opts coverage.FetchOptions) ([]string, error) {
	region := "us-east-1"
	if len(opts.Regions) > 0 {
		region = opts.Regions[0]
	}
	cfgOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if opts.Profile != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := ec2.NewFromConfig(cfg)
	allRegions := true
	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
		Filters: []ec2types.Filter{{
			Name:   sp("opt-in-status"),
			Values: []string{"opt-in-not-required", "opted-in"},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("ec2:DescribeRegions: %w", err)
	}
	regions := make([]string, 0, len(out.Regions))
	for _, r := range out.Regions {
		if r.RegionName != nil {
			regions = append(regions, *r.RegionName)
		}
	}
	return regions, nil
}
