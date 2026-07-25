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
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for AWS. Upstream truth
// source = CloudFormation ListTypes (Visibility=Public, Type=Resource) unioned
// with the credential-free AWS Service Reference catalog (see Fetch).
// Coverage truth source = CollectEmits() in aws_services.go, which unions
// every registerService emits decl plus extraEmits from resolver-side
// synthetic stubs.
type coverageProvider struct{}

func (coverageProvider) Name() string { return "aws" }

// Emits returns CollectEmits() verbatim — the Leaf flag on each TypeDecl is
// set at registration time alongside the scanner's emits decl, keeping the
// decision next to the SDK-shape author who knows whether the type carries
// outbound refs.
func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

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

// Aliases returns the disco-type -> upstream-CFN/Service-Reference key
// overrides declared per-type via registerType (restype.Descriptor.Upstream).
// A type with no Upstream falls through to the algorithmic key. New
// overrides are set on the type's descriptor, not here.
func (coverageProvider) Aliases() map[string]string {
	return descriptorAliases()
}

// AlgorithmicKey is the fallback when no alias entry exists. Disco type
// "aws:foo:bar-baz" → "AWS::Foo::BarBaz". The alias map handles the cases
// where this fails (any disco service segment that doesn't map cleanly to
// CFN's segment, e.g. logs vs Logs, ses vs SES, plus "aws prefix missing" or
// different-case oddities).
func (coverageProvider) AlgorithmicKey(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) != 3 {
		return discoType
	}
	svc, kind := parts[1], parts[2]
	pascal := func(s string) string {
		segs := strings.Split(s, "-")
		for i, p := range segs {
			if p == "" {
				continue
			}
			segs[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		return strings.Join(segs, "")
	}
	return "AWS::" + pascal(svc) + "::" + pascal(kind)
}

// canonService normalizes a service segment: lowercase, hyphens/underscores
// stripped, then a serviceRenames bridge for the few services CFN and the
// Service Reference name differently beyond case/hyphen. Services are NOT
// de-pluralized — many legitimately end in "s" (aidevops, logs, ecs, sns),
// and CFN/SR agree on the plural ("Logs"↔"logs"), so stripping it would
// desync the two spellings.
func canonService(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	if r, ok := serviceRenames[s]; ok {
		s = r
	}
	return s
}

// canonResource normalizes a resource segment: lowercase, hyphens/underscores
// stripped, de-pluralized, and the Service-Reference "Resource" suffix removed
// when a non-empty stem remains (so "Resources" stays "resource", never
// collapses to ""). Singularization is intentionally crude — it only has to be
// *consistent* across the two spellings of one resource, not linguistically
// correct, since the result is an internal matching identity, never displayed.
func canonResource(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	switch {
	case strings.HasSuffix(s, "ies"): // policies → policy
		s = s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"), // addresses → address
		strings.HasSuffix(s, "ches"), // branches → branch
		strings.HasSuffix(s, "shes"), // meshes → mesh
		strings.HasSuffix(s, "xes"),  // boxes → box
		strings.HasSuffix(s, "zes"):  // quizzes → quiz
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "ss"):
		// keep — "access", "address" are not plurals.
	case strings.HasSuffix(s, "s"):
		s = s[:len(s)-1]
	}
	if stem := strings.TrimSuffix(s, "resource"); stem != "" && stem != s {
		s = stem
	}
	return s
}

// serviceRenames bridges the few services CloudFormation and the Service
// Reference name differently beyond mere case/hyphen (CFN "MWAA" vs SR
// "airflow"). Keyed and valued in canonService form (lowercase, no hyphens).
// Extend as the A→Z buildout surfaces more genuine renames — the canonicalizer
// handles every other case.
var serviceRenames = map[string]string{
	"airflow":                      "mwaa",                   // SR airflow ↔ CFN MWAA
	"airflowserverless":            "mwaaserverless",         // SR airflow-serverless ↔ CFN MWAAServerless
	"acm":                          "certificatemanager",     // SR acm ↔ CFN CertificateManager
	"devopsagent":                  "aidevops",               // CFN DevOpsAgent ↔ SR aidevops
	"aoss":                         "opensearchserverless",   // SR aoss ↔ CFN OpenSearchServerless
	"codeconnections":              "codestarconnections",    // SR codeconnections ↔ scanned aws:codestar-connections (AWS renamed the service)
	"cognitoidentity":              "cognito",                // SR cognito-identity (identity pools) ↔ unified CFN/scanned Cognito
	"cognitoidp":                   "cognito",                // SR cognito-idp (user pools) ↔ unified CFN/scanned Cognito
	"elasticfilesystem":            "efs",                    // SR elasticfilesystem ↔ CFN EFS / scanned aws:efs
	"elasticmapreduce":             "emr",                    // SR elasticmapreduce ↔ CFN EMR / scanned aws:emr
	"firehose":                     "kinesisfirehose",        // SR firehose ↔ CFN KinesisFirehose / scanned aws:firehose
	"geo":                          "location",               // SR geo ↔ CFN Location / scanned aws:location
	"kafka":                        "msk",                    // SR kafka ↔ CFN MSK / scanned aws:kafka
	"medicalimaging":               "healthimaging",          // SR medical-imaging ↔ CFN HealthImaging / scanned aws:health-imaging
	"mgh":                          "migrationhub",           // SR mgh ↔ scanned aws:migrationhub (SDK service migrationhub)
	"opensearch":                   "opensearchservice",      // SR opensearch ↔ CFN OpenSearchService / scanned aws:opensearchservice
	"es":                           "opensearchservice",      // legacy Elasticsearch IAM prefix ↔ CFN OpenSearchService
	"profile":                      "customerprofiles",       // SR profile ↔ CFN CustomerProfiles / scanned aws:customer-profiles
	"route53recoverycontrolconfig": "route53recoverycontrol", // SR config API ↔ scanned aws:route53-recovery-control
	"schemas":                      "eventschemas",           // SR schemas (EventBridge Schemas) ↔ CFN EventSchemas / scanned aws:event-schemas
	"ssmsap":                       "systemsmanagersap",      // SR ssm-sap ↔ scanned aws:systems-manager-sap (CFN SystemsManagerSAP)
}

// CanonicalKey normalizes an "AWS::svc::res" upstream key to a catalog-agnostic
// identity so a CloudFormation spelling and its Service-Reference twin collapse
// to one resource (e.g. AWS::Amplify::App and AWS::amplify::apps both →
// "amplify::app"). coverage.Build uses it to treat an uncovered upstream key as
// covered when its identity matches an already-covered key — the cross-catalog
// duplicate case. The covered-vs-leftover asymmetry (one side is a disco-emitted
// alias target, the other an unmatched catalog entry) is what scopes the merge;
// `coverage services --filter duplicate` surfaces every collapse for audit.
func (coverageProvider) CanonicalKey(upstreamKey string) string {
	parts := strings.SplitN(upstreamKey, "::", 3)
	if len(parts) != 3 {
		return strings.ToLower(upstreamKey)
	}
	return canonService(parts[1]) + "::" + canonResource(parts[2])
}

// Fetch returns the union of CloudFormation ListTypes (Public, Resource) and
// the AWS Service Reference catalog. CFN supplies registry-modeled resources;
// Service Reference supplies the SDK-real resources CFN omits. Third-party CFN
// types (community / Hooks / Modules) are filtered out — not relevant to
// disco's coverage matrix.
func (coverageProvider) Fetch(ctx context.Context, opts coverage.FetchOptions) ([]coverage.UpstreamType, error) {
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
	// silently re-introduce false upstream-missing rows. coverage.Build dedupes
	// the overlap case-insensitively.
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
