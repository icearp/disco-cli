package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	apprunnertypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// The model marks runtime environment variables, StartCommand and
	// BuildCommand sensitive. A variable is kept only when its value is a
	// safe shape (the amplify EnvironmentVariables precedent); a command is
	// a setting, kept unless it holds a credential shape.
	registerType(restype.Descriptor{Type: TypeAppRunnerService, Service: "apprunner", Redact: []redact.Rule{
		{Path: "SourceConfiguration.ImageRepository.ImageConfiguration.RuntimeEnvironmentVariables.*", Mode: redact.RedactUnlessSafe},
		{Path: "SourceConfiguration.ImageRepository.ImageConfiguration.StartCommand", Mode: redact.RedactIfSensitive},
		{Path: "SourceConfiguration.CodeRepository.CodeConfiguration.CodeConfigurationValues.RuntimeEnvironmentVariables.*", Mode: redact.RedactUnlessSafe},
		{Path: "SourceConfiguration.CodeRepository.CodeConfiguration.CodeConfigurationValues.StartCommand", Mode: redact.RedactIfSensitive},
		{Path: "SourceConfiguration.CodeRepository.CodeConfiguration.CodeConfigurationValues.BuildCommand", Mode: redact.RedactIfSensitive},
	}})
	registerType(restype.Descriptor{Type: TypeAppRunnerVPCConnector, Service: "apprunner"})
	registerType(restype.Descriptor{Type: TypeAppRunnerAutoScalingConfiguration, Service: "apprunner"})
	registerType(restype.Descriptor{Type: TypeAppRunnerObservabilityConfiguration, Service: "apprunner"})
	registerType(restype.Descriptor{Type: TypeAppRunnerVpcIngressConnection, Service: "apprunner"})
	registerType(restype.Descriptor{Type: TypeAppRunnerConnection, Service: "apprunner"})
	// CertificateValidationRecords are the CNAME name/value pairs the owner
	// publishes in public DNS for ACM validation, not credentials; no redaction.
	registerType(restype.Descriptor{Type: TypeAppRunnerCustomDomain, Service: "apprunner"})
	registerService(serviceEntry{
		name: "aws:apprunner",
		fn:   scanAppRunner,
	})
}

// errAppRunnerNoServiceBody is the error of a DescribeService response that
// lacks its model-required Service.
var errAppRunnerNoServiceBody = errors.New("DescribeService response has no Service")

// appRunnerNotFound is the code DescribeService, DescribeCustomDomains and
// ListTagsForResource model for a resource that does not exist.
var appRunnerNotFound = []string{"ResourceNotFoundException"}

const (
	appRunnerListServicesOp          = "apprunner:ListServices"
	appRunnerDescribeServiceOp       = "apprunner:DescribeService"
	appRunnerDescribeCustomDomainsOp = "apprunner:DescribeCustomDomains"
	appRunnerTagsOp                  = "apprunner:ListTagsForResource"
	// appRunnerCustomDomainPageLimit is the model's cap on DescribeCustomDomains
	// MaxResults (range 1..5).
	appRunnerCustomDomainPageLimit int32 = 5
	// appRunnerServicePageLimit is the model's cap on ListServices MaxResults
	// (range 1..20).
	appRunnerServicePageLimit int32 = 20
	// appRunnerPageLimit is the model's cap on the MaxResults of the other
	// listings (range 1..100).
	appRunnerPageLimit int32 = 100
)

// apprunnerAPI is the narrow set of App Runner operations called by the
// scanAppRunner sub-phases.
type apprunnerAPI interface {
	ListServices(context.Context, *apprunner.ListServicesInput, ...func(*apprunner.Options)) (*apprunner.ListServicesOutput, error)
	DescribeService(context.Context, *apprunner.DescribeServiceInput, ...func(*apprunner.Options)) (*apprunner.DescribeServiceOutput, error)
	ListVpcConnectors(context.Context, *apprunner.ListVpcConnectorsInput, ...func(*apprunner.Options)) (*apprunner.ListVpcConnectorsOutput, error)
	ListAutoScalingConfigurations(context.Context, *apprunner.ListAutoScalingConfigurationsInput, ...func(*apprunner.Options)) (*apprunner.ListAutoScalingConfigurationsOutput, error)
	ListObservabilityConfigurations(context.Context, *apprunner.ListObservabilityConfigurationsInput, ...func(*apprunner.Options)) (*apprunner.ListObservabilityConfigurationsOutput, error)
	ListVpcIngressConnections(context.Context, *apprunner.ListVpcIngressConnectionsInput, ...func(*apprunner.Options)) (*apprunner.ListVpcIngressConnectionsOutput, error)
	ListConnections(context.Context, *apprunner.ListConnectionsInput, ...func(*apprunner.Options)) (*apprunner.ListConnectionsOutput, error)
	DescribeCustomDomains(context.Context, *apprunner.DescribeCustomDomainsInput, ...func(*apprunner.Options)) (*apprunner.DescribeCustomDomainsOutput, error)
	ListTagsForResource(context.Context, *apprunner.ListTagsForResourceInput, ...func(*apprunner.Options)) (*apprunner.ListTagsForResourceOutput, error)
}

// scanAppRunner discovers the App Runner resources of one region. Every
// listing runs as its own phase, one at a time, in the order below
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. The custom-domain phase gets the services the
// service phase stored. Every listing but the custom domains (which have no
// ARN) is written a page at a time with each row's tags read
// (appRunnerTags), and the tag denials of the whole scan are one warning.
func scanAppRunner(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := apprunner.NewFromConfig(acct.cfg, func(o *apprunner.Options) { o.Region = region })
	tags := appRunnerTags(client).sharingDenials()
	var services []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "apprunner", 0, []hostPhase{
		{op: appRunnerListServicesOp, run: func(p *phaseTotals) (failed bool) {
			services, failed = scanAppRunnerServices(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "apprunner:ListVpcConnectors", run: func(p *phaseTotals) bool {
			return scanAppRunnerVPCConnectors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "apprunner:ListAutoScalingConfigurations", run: func(p *phaseTotals) bool {
			return scanAppRunnerAutoScalingConfigs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "apprunner:ListObservabilityConfigurations", run: func(p *phaseTotals) bool {
			return scanAppRunnerObservabilityConfigs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "apprunner:ListVpcIngressConnections", run: func(p *phaseTotals) bool {
			return scanAppRunnerVpcIngressConnections(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "apprunner:ListConnections", run: func(p *phaseTotals) bool {
			return scanAppRunnerConnections(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, appRunnerDescribeCustomDomainsOp, func() (int, int, error) {
			return scanAppRunnerCustomDomains(ctx, client, acct, region, st, scanID, services)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// appRunnerTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The op returns every tag in one
// response (it takes no page token); ResourceNotFoundException, which it
// models, means the resource was deleted after it was listed.
//
// The Service Reference lists every App Runner type this scanner tags under
// ListTagsForResource except vpcingressconnection, which it lists under
// TagResource and UntagResource. The op's model takes any App Runner ARN
// (its ResourceArn pattern names no resource type) and CreateVpcIngressConnection
// takes Tags, so VPC ingress connections are read too. Unverified live.
//
// An AWS-owned row (ManagedByProvider: the DefaultConfiguration
// observability configuration, a name App Runner reserves) is not read:
// whether the op accepts its ARN is unverified, and a refusal would fail
// every scan. Such a row keeps the TagsJSON its builder set.
func appRunnerTags(client apprunnerAPI) rowTagReader {
	return rowTagReader{
		op:       appRunnerTagsOp,
		notFound: appRunnerNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &apprunner.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t apprunnertypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// appRunnerCustomDomainNativeID keys a custom domain under its service: the API
// returns no ARN for it and the Service Reference defines none. The domain
// name's model pattern (^[A-Za-z0-9*.-]{1,255}$) admits no "/", and it goes
// last all the same.
func appRunnerCustomDomainNativeID(serviceARN, domain string) string {
	return serviceARN + "/custom-domain/" + domain
}

// isAppRunnerServiceGone reports whether err says the service no longer
// exists, i.e. it was deleted after ListServices listed it.
func isAppRunnerServiceGone(err error) bool {
	return isAPIErrorCode(err, appRunnerNotFound...)
}

// scanAppRunnerCustomDomains stores each service's custom domains, a page at
// a time, and links each under its service. services holds only services
// stored this scan. Per service: ResourceNotFoundException (deleted since
// listing) is skipped silently, a denial warns once, a transient error after
// the SDK's retries is skipped with one warning for all such services,
// InvalidRequestException is skipped with one warning for all services
// refused with it, and anything else fails the phase. Rows a service stored
// before its error stay and are counted.
//
// The model documents InvalidRequestException as one or more invalid input
// parameters. Skipping it is a guess: a stored service is not filtered on its
// Status, which may be CREATE_FAILED, DELETED or OPERATION_IN_PROGRESS, and
// whether DescribeCustomDomains refuses such a service with this code has not
// been observed. Failing the phase would then fail it on every scan, and
// skipping silently would hide a real input error, so it warns. The inputs
// disco sets are the listed service ARN and a page size pinned to the model
// cap by a test.
//
// The response's DNSTarget and VpcDNSTargets describe the service, not one
// domain, so they are not stored on the domain rows.
func scanAppRunnerCustomDomains(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, services []childParent) (int, int, error) {
	return childFanOutStoredSkipNotReady(ctx, st, acct, region, appRunnerDescribeCustomDomainsOp, services, isAppRunnerServiceGone, byParentARN,
		"service(s)", "a service in a status such as CREATE_FAILED, DELETED or OPERATION_IN_PROGRESS may refuse the call",
		[]string{"InvalidRequestException"},
		func(ctx context.Context, svc childParent) (int, int, error) {
			return storeAppRunnerCustomDomains(ctx, client, acct, region, st, scanID, svc)
		})
}

// storeAppRunnerCustomDomains stores one service's custom domains a page at a time.
func storeAppRunnerCustomDomains(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, svc childParent) (total, inserted int, err error) {
	pager := apprunner.NewDescribeCustomDomainsPaginator(client,
		&apprunner.DescribeCustomDomainsInput{ServiceArn: &svc.arn},
		func(o *apprunner.DescribeCustomDomainsPaginatorOptions) {
			o.Limit = appRunnerCustomDomainPageLimit
			o.StopOnDuplicateToken = true
		})
	parentID := store.ResourceID("aws", acct.ID, svc.arn)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		var children []containedChild
		for _, d := range out.CustomDomains {
			domain := sv(d.DomainName)
			if domain == "" {
				continue
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerCustomDomain, NativeID: appRunnerCustomDomainNativeID(svc.arn, domain),
				Name: &domain, Region: &region, Status: nonEmptyPtr(string(d.Status)),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}})
		}
		t, i, err := upsertContained(st, appRunnerDescribeCustomDomainsOp, children)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanAppRunnerConnections stores the source-code provider connections
// (GitHub / Bitbucket links, account-scoped per region) under their ARNs.
func scanAppRunnerConnections(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "apprunner:ListConnections",
		func(ctx context.Context, token *string) ([]apprunnertypes.ConnectionSummary, *string, error) {
			out, err := client.ListConnections(ctx, &apprunner.ListConnectionsInput{MaxResults: sdkaws.Int32(appRunnerPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectionSummaryList, out.NextToken, nil
		},
		func(c apprunnertypes.ConnectionSummary) (*store.Resource, string, string) {
			name := sv(c.ConnectionName)
			return arnRow("connections", c.ConnectionArn, c.ConnectionName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerConnection, NativeID: sv(c.ConnectionArn),
				Name: &name, Region: &region, CreatedAt: tp(c.CreatedAt),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanAppRunnerAutoScalingConfigs stores the auto-scaling configuration
// revisions (account-scoped per region) under their ARNs. The
// DefaultConfiguration App Runner provides is read like any other: the
// CreateAutoScalingConfiguration doc says it is managed the same way as a
// custom one (new revisions, deletion). An INACTIVE revision's tags are not
// read (appRunnerUnlessInactive): the listing's doc says it returns active
// revisions only, but the summary carries a Status whose model doc says an
// INACTIVE revision was deleted and stays until App Runner removes it.
func scanAppRunnerAutoScalingConfigs(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "apprunner:ListAutoScalingConfigurations",
		func(ctx context.Context, token *string) ([]apprunnertypes.AutoScalingConfigurationSummary, *string, error) {
			out, err := client.ListAutoScalingConfigurations(ctx, &apprunner.ListAutoScalingConfigurationsInput{MaxResults: sdkaws.Int32(appRunnerPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AutoScalingConfigurationSummaryList, out.NextToken, nil
		},
		func(c apprunnertypes.AutoScalingConfigurationSummary) (*store.Resource, string, string) {
			name := sv(c.AutoScalingConfigurationName)
			return arnRow("auto scaling configurations", c.AutoScalingConfigurationArn, c.AutoScalingConfigurationName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerAutoScalingConfiguration, NativeID: sv(c.AutoScalingConfigurationArn),
				Name: &name, Region: &region, Status: nonEmptyPtr(string(c.Status)), CreatedAt: tp(c.CreatedAt),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, appRunnerUnlessInactive(tags, string(apprunnertypes.AutoScalingConfigurationStatusInactive)), p)
}

// scanAppRunnerObservabilityConfigs stores the observability configuration
// revisions (account-scoped per region) under their ARNs.
func scanAppRunnerObservabilityConfigs(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "apprunner:ListObservabilityConfigurations",
		func(ctx context.Context, token *string) ([]apprunnertypes.ObservabilityConfigurationSummary, *string, error) {
			out, err := client.ListObservabilityConfigurations(ctx, &apprunner.ListObservabilityConfigurationsInput{MaxResults: sdkaws.Int32(appRunnerPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ObservabilityConfigurationSummaryList, out.NextToken, nil
		},
		func(c apprunnertypes.ObservabilityConfigurationSummary) (*store.Resource, string, string) {
			name := sv(c.ObservabilityConfigurationName)
			return arnRow("observability configurations", c.ObservabilityConfigurationArn, c.ObservabilityConfigurationName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerObservabilityConfiguration, NativeID: sv(c.ObservabilityConfigurationArn),
				Name: &name, Region: &region,
				// Account-scoped AWS-supplied default named "DefaultConfiguration"
				// (no IsDefault field on this summary type — name is the only signal).
				ManagedByProvider: name == "DefaultConfiguration",
				AttributesJSON:    mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanAppRunnerVpcIngressConnections stores the VPC ingress connections
// (account-scoped per region) under their ARNs. VpcIngressConnectionSummary
// carries the linked ServiceArn for the resolver edge, and no name.
func scanAppRunnerVpcIngressConnections(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "apprunner:ListVpcIngressConnections",
		func(ctx context.Context, token *string) ([]apprunnertypes.VpcIngressConnectionSummary, *string, error) {
			out, err := client.ListVpcIngressConnections(ctx, &apprunner.ListVpcIngressConnectionsInput{MaxResults: sdkaws.Int32(appRunnerPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VpcIngressConnectionSummaryList, out.NextToken, nil
		},
		func(c apprunnertypes.VpcIngressConnectionSummary) (*store.Resource, string, string) {
			arn := sv(c.VpcIngressConnectionArn)
			return arnRow("VPC ingress connections", c.VpcIngressConnectionArn, nil, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerVpcIngressConnection, NativeID: arn,
				Name: &arn, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanAppRunnerServices stores the services a page at a time, each row's
// attributes replaced with its DescribeService body (NetworkConfiguration,
// SourceConfiguration, EncryptionConfiguration, InstanceConfiguration), and
// returns every service stored, as parents for the custom-domain phase. A
// service DescribeService no longer finds was deleted since the listing and
// is dropped; any other failure, a response without its model-required
// Service included, keeps the ServiceSummary as the row (rowDetailReader).
// Name and Status come from the ServiceSummary.
func scanAppRunnerServices(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (services []childParent, storeWriteFailed bool) {
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: appRunnerDescribeServiceOp, what: "service(s)", notFound: appRunnerNotFound,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				arn := r.NativeID
				out, err := client.DescribeService(ctx, &apprunner.DescribeServiceInput{ServiceArn: &arn})
				if err != nil {
					return nil, err
				}
				if out.Service == nil {
					return nil, errAppRunnerNoServiceBody
				}
				return out.Service, nil
			},
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				services = append(services, childParent{id: r.NativeID, arn: r.NativeID})
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, appRunnerListServicesOp,
		func(ctx context.Context, token *string) ([]apprunnertypes.ServiceSummary, *string, error) {
			out, err := client.ListServices(ctx, &apprunner.ListServicesInput{MaxResults: sdkaws.Int32(appRunnerServicePageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ServiceSummaryList, out.NextToken, nil
		},
		func(s apprunnertypes.ServiceSummary) (*store.Resource, string, string) {
			name := sv(s.ServiceName)
			status := string(s.Status)
			return arnRow("services", s.ServiceArn, s.ServiceName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerService, NativeID: sv(s.ServiceArn),
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
	return services, failed
}

// scanAppRunnerVPCConnectors stores the VPC connector revisions (full body:
// Subnets, SecurityGroups) under their ARNs. An INACTIVE revision was
// deleted (the model's Status doc) but is still listed until App Runner
// removes it, so its tags are not read (appRunnerUnlessInactive).
func scanAppRunnerVPCConnectors(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "apprunner:ListVpcConnectors",
		func(ctx context.Context, token *string) ([]apprunnertypes.VpcConnector, *string, error) {
			out, err := client.ListVpcConnectors(ctx, &apprunner.ListVpcConnectorsInput{MaxResults: sdkaws.Int32(appRunnerPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VpcConnectors, out.NextToken, nil
		},
		func(c apprunnertypes.VpcConnector) (*store.Resource, string, string) {
			name := sv(c.VpcConnectorName)
			status := string(c.Status)
			return arnRow("VPC connectors", c.VpcConnectorArn, c.VpcConnectorName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppRunnerVPCConnector, NativeID: sv(c.VpcConnectorArn),
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, appRunnerUnlessInactive(tags, string(apprunnertypes.VpcConnectorStatusInactive)), p)
}

// appRunnerUnlessInactive is tags without a read for a row whose Status is
// inactive, a deleted revision App Runner still lists: whether the op
// answers for a deleted revision is unverified, and a not-found answer would
// drop a row the listing returned. Such a row is stored without tags.
func appRunnerUnlessInactive(tags rowTagReader, inactive string) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if sv(r.Status) == inactive {
			return nil, nil
		}
		return read(ctx, r)
	}
	return tags
}
