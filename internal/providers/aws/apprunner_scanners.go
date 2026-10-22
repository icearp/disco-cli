package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppRunnerService, Service: "apprunner"})
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

// errAppRunnerNoServiceBody is the warning cause for a DescribeService
// response that lacks its model-required Service.
var errAppRunnerNoServiceBody = errors.New("DescribeService response has no Service")

const (
	appRunnerDescribeServiceOp       = "apprunner:DescribeService"
	appRunnerDescribeCustomDomainsOp = "apprunner:DescribeCustomDomains"
	// appRunnerCustomDomainPageLimit is the model's cap on DescribeCustomDomains
	// MaxResults (range 1..5).
	appRunnerCustomDomainPageLimit int32 = 5
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
}

// scanAppRunner discovers App Runner resources in one region, then the custom
// domains of every service it stored. The custom-domain phase runs whatever the
// earlier phases returned, so its failure costs them nothing. The earlier
// phases' transient error is demoted to a warning only beside a hard error:
// joined, it would be buried in the hard error instead of reported under its
// op; the counts are summed.
func scanAppRunner(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := apprunner.NewFromConfig(acct.cfg, func(o *apprunner.Options) { o.Region = region })
	total, inserted, services, err := scanAppRunnerPhases(ctx, client, acct, region, st, scanID)
	t, i, derr := scanAppRunnerCustomDomains(ctx, client, acct, region, st, scanID, services)
	return total + t, inserted + i, demoteBeside(st, "apprunner", acct, region, err, derr)
}

// scanAppRunnerPhases runs, stopping at the first error: ListServices
// (paginator, skeleton) → fan-out DescribeService for full body
// (NetworkConfiguration, SourceConfiguration, EncryptionConfiguration,
// InstanceConfiguration); ListVpcConnectors (full body — Subnets,
// SecurityGroups); auto-scaling configs; observability configs; VPC ingress
// connections; source-provider connections. Per-phase + per-item AccessDenied
// tolerated. It returns the services stored, even when a later phase fails.
func scanAppRunnerPhases(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, services []childParent, err error) {
	total, inserted, services, err = scanAppRunnerServices(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, services, err
	}
	for _, phase := range []func(context.Context, apprunnerAPI, *account, string, *store.Store, string) (int, int, error){
		scanAppRunnerVPCConnectors,
		scanAppRunnerAutoScalingConfigs,
		scanAppRunnerObservabilityConfigs,
		scanAppRunnerVpcIngressConnections,
		scanAppRunnerConnections,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, services, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, services, nil
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
	return isAPIErrorCode(err, "ResourceNotFoundException")
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

// scanAppRunnerConnections lists source-code provider connections (GitHub /
// Bitbucket links, account-scoped per region). NativeID = ConnectionArn.
func scanAppRunnerConnections(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListConnectionsPaginator(client, &apprunner.ListConnectionsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListConnections", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListConnections: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.ConnectionSummaryList))
		for _, c := range out.ConnectionSummaryList {
			arn := sv(c.ConnectionArn)
			if arn == "" {
				continue
			}
			name := sv(c.ConnectionName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerConnection,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(c.CreatedAt),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner connections: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanAppRunnerAutoScalingConfigs lists auto-scaling configuration revisions
// (account-scoped per region). NativeID = AutoScalingConfigurationArn.
func scanAppRunnerAutoScalingConfigs(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListAutoScalingConfigurationsPaginator(client, &apprunner.ListAutoScalingConfigurationsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListAutoScalingConfigurations", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListAutoScalingConfigurations: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.AutoScalingConfigurationSummaryList))
		for _, c := range out.AutoScalingConfigurationSummaryList {
			arn := sv(c.AutoScalingConfigurationArn)
			if arn == "" {
				continue
			}
			name := sv(c.AutoScalingConfigurationName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerAutoScalingConfiguration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(c.CreatedAt),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner autoscaling-configs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanAppRunnerObservabilityConfigs lists observability config revisions
// (account-scoped per region). NativeID = ObservabilityConfigurationArn.
func scanAppRunnerObservabilityConfigs(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListObservabilityConfigurationsPaginator(client, &apprunner.ListObservabilityConfigurationsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListObservabilityConfigurations", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListObservabilityConfigurations: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.ObservabilityConfigurationSummaryList))
		for _, c := range out.ObservabilityConfigurationSummaryList {
			arn := sv(c.ObservabilityConfigurationArn)
			if arn == "" {
				continue
			}
			name := sv(c.ObservabilityConfigurationName)
			batch = append(batch, &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeAppRunnerObservabilityConfiguration,
				NativeID:    arn,
				Name:        &name,
				Region:      &region,
				// Account-scoped AWS-supplied default named "DefaultConfiguration"
				// (no IsDefault field on this summary type — name is the only signal).
				ManagedByProvider: name == "DefaultConfiguration",
				AttributesJSON:    mustJSON(c),
				DiscoveredBy:      scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner observability-configs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanAppRunnerVpcIngressConnections lists VPC ingress connections
// (account-scoped per region). NativeID = VpcIngressConnectionArn.
// VpcIngressConnectionSummary carries the linked ServiceArn for resolver edge.
func scanAppRunnerVpcIngressConnections(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListVpcIngressConnectionsPaginator(client, &apprunner.ListVpcIngressConnectionsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListVpcIngressConnections", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListVpcIngressConnections: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.VpcIngressConnectionSummaryList))
		for _, c := range out.VpcIngressConnectionSummaryList {
			arn := sv(c.VpcIngressConnectionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerVpcIngressConnection,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner vpc-ingress-connections: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAppRunnerServices(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, services []childParent, err error) {
	pager := apprunner.NewListServicesPaginator(client, &apprunner.ListServicesInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "apprunner:ListServices", acct.ID, region, perr)
				return 0, 0, nil, nil
			}
			return 0, 0, nil, fmt.Errorf("apprunner:ListServices: %w", perr)
		}
		for _, s := range out.ServiceSummaryList {
			if s.ServiceArn != nil {
				arns = append(arns, *s.ServiceArn)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil, nil
	}

	// A service deleted since ListServices is dropped silently; a denial drops
	// the service and warns once. The model requires Service on the response,
	// so a response without it is dropped with one warning for all such
	// services rather than silently. A service whose call still fails
	// transiently after the SDK's retries is dropped with one warning for all
	// such services, so one throttled call costs neither its siblings' rows nor
	// the later phases.
	var (
		noBody    skipTally
		transient transientTally
	)
	batch, derr := describeFanOut(ctx, st, acct, region, appRunnerDescribeServiceOp, arns, isAppRunnerServiceGone,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.DescribeService(ctx, &apprunner.DescribeServiceInput{ServiceArn: &arn})
			if transient.drop(arn, err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			if out.Service == nil {
				noBody.skipped(arn, errAppRunnerNoServiceBody)
				return nil, nil
			}
			name := sv(out.Service.ServiceName)
			status := string(out.Service.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerService,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.Service),
				DiscoveredBy:   scanID,
			}, nil
		})
	transient.warnSkipped(st, appRunnerDescribeServiceOp, acct, region)
	if noBody.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: appRunnerDescribeServiceOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d service(s) skipped: %v, first %s", noBody.count, noBody.err, noBody.first),
		})
	}
	if derr != nil {
		return 0, 0, nil, derr
	}
	if len(batch) == 0 {
		return 0, 0, nil, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert apprunner services: %w", uerr)
	}
	services = make([]childParent, len(batch))
	for i, r := range batch {
		services[i] = childParent{id: r.NativeID, arn: r.NativeID}
	}
	return len(batch), n, services, nil
}

func scanAppRunnerVPCConnectors(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListVpcConnectorsPaginator(client, &apprunner.ListVpcConnectorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "apprunner:ListVpcConnectors", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apprunner:ListVpcConnectors: %w", perr)
		}
		for _, c := range out.VpcConnectors {
			arn := sv(c.VpcConnectorArn)
			if arn == "" {
				continue
			}
			name := sv(c.VpcConnectorName)
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerVPCConnector,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert apprunner vpc-connectors: %w", uerr)
	}
	return len(batch), n, nil
}
