package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
	applicationsignalstypes "github.com/aws/aws-sdk-go-v2/service/applicationsignals/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeApplicationSignalsSLO, Service: "applicationsignals"})
	registerType(restype.Descriptor{Type: TypeApplicationSignalsGroupingConfiguration, Service: "applicationsignals"})
	registerType(restype.Descriptor{Type: TypeCloudWatchService, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeApplicationSignalsInstrumentationConfiguration, Service: "applicationsignals"})
	registerService(serviceEntry{
		name: "aws:applicationsignals",
		fn:   scanApplicationSignals,
	})
}

const (
	appSignalsListServicesOp                            = "applicationsignals:ListServices"
	appSignalsListInstrumentationConfigurationsOp       = "applicationsignals:ListInstrumentationConfigurations"
	appSignalsInstrumentationPageLimit            int32 = 100 // model cap (MaxResults 1..100)
)

type applicationSignalsAPI interface {
	ListServiceLevelObjectives(context.Context, *applicationsignals.ListServiceLevelObjectivesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error)
	ListGroupingAttributeDefinitions(context.Context, *applicationsignals.ListGroupingAttributeDefinitionsInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error)
	ListServices(context.Context, *applicationsignals.ListServicesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServicesOutput, error)
	ListInstrumentationConfigurations(context.Context, *applicationsignals.ListInstrumentationConfigurationsInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListInstrumentationConfigurationsOutput, error)
}

// applicationSignalsService is an instrumented service stored by the services
// phase: the name and environment ListInstrumentationConfigurations keys on,
// and the service row's NativeID.
type applicationSignalsService struct{ name, environment, nativeID string }

// applicationSignalsServiceNativeID synthesizes a stable NativeID for an
// Application Signals service. The SDK exposes no ARN — identity is the
// required KeyAttributes map (Type/Name/Environment/…). Sorting into "k=v"
// pairs keeps the NativeID stable regardless of Go's map-iteration order.
func applicationSignalsServiceNativeID(region, acct string, keyAttrs map[string]string) string {
	keys := make([]string, 0, len(keyAttrs))
	for k := range keyAttrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+keyAttrs[k])
	}
	return fmt.Sprintf("arn:aws:application-signals:%s:%s:service/%s", region, acct, strings.Join(pairs, ";"))
}

// applicationSignalsGroupingConfigurationNativeID synthesizes the singleton
// per-(account, region) NativeID for the grouping configuration. The SDK
// exposes no ARN; body enumerated via ListGroupingAttributeDefinitions.
func applicationSignalsGroupingConfigurationNativeID(region, acct string) string {
	return fmt.Sprintf("arn:aws:application-signals:%s:%s:grouping-configuration", region, acct)
}

// scanApplicationSignals runs the SLO, grouping-configuration and services
// phases, then lists the instrumentation configurations of every instrumented
// service the services phase stored, whatever that phase returned. The
// services phase's transient error is demoted to a warning, since one
// transient member would make the dispatcher read the whole join as
// transient; the counts are summed.
func scanApplicationSignals(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := applicationsignals.NewFromConfig(acct.cfg, func(o *applicationsignals.Options) { o.Region = region })
	sloTotal, sloInserted, err := scanApplicationSignalsSLOs(ctx, client, acct, region, st, scanID)
	if err != nil {
		return sloTotal, sloInserted, err
	}
	gcTotal, gcInserted, err := scanApplicationSignalsGroupingConfiguration(ctx, client, acct, region, st, scanID)
	if err != nil {
		return sloTotal + gcTotal, sloInserted + gcInserted, err
	}
	total, inserted = sloTotal+gcTotal, sloInserted+gcInserted
	t, i, services, serr := scanApplicationSignalsServices(ctx, client, acct, region, st, scanID, time.Now())
	total, inserted = total+t, inserted+i
	t, i, ierr := scanApplicationSignalsInstrumentationConfigurations(ctx, client, acct, region, st, scanID, services)
	return total + t, inserted + i, errors.Join(demoteTransient(st, appSignalsListServicesOp, acct, region, serr), ierr)
}

// scanApplicationSignalsServices lists Application Signals services seen in
// the trailing 24h. ListServices requires a time window and returns only
// services with telemetry in it — a service quiet longer than the window
// won't appear (inherent API limit). Services carry no ARN; NativeID is
// synthesized from KeyAttributes. It returns the instrumented services
// (KeyAttributes Type "Service") stored before any failure.
func scanApplicationSignalsServices(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string, now time.Time) (total, inserted int, services []applicationSignalsService, err error) {
	start := now.Add(-24 * time.Hour)
	in := &applicationsignals.ListServicesInput{
		StartTime: &start,
		EndTime:   &now,
	}
	p := applicationsignals.NewListServicesPaginator(client, in)
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, services, skipIfAccessDenied(st, appSignalsListServicesOp, acct.ID, region, perr)
			}
			return total, inserted, services, fmt.Errorf("%s: %w", appSignalsListServicesOp, perr)
		}
		batch := make([]*store.Resource, 0, len(page.ServiceSummaries))
		var instrumented []applicationSignalsService
		for _, s := range page.ServiceSummaries {
			if len(s.KeyAttributes) == 0 {
				continue
			}
			name := s.KeyAttributes["Name"]
			nativeID := applicationSignalsServiceNativeID(region, acct.ID, s.KeyAttributes)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchService,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
			// Only Type "Service" names an application reporting its own
			// service.name, which is what an instrumentation configuration
			// targets; RemoteService and AWS::Service are callees seen from it.
			env := s.KeyAttributes["Environment"]
			if s.KeyAttributes["Type"] == "Service" && name != "" && env != "" {
				instrumented = append(instrumented, applicationSignalsService{name: name, environment: env, nativeID: nativeID})
			}
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, services, fmt.Errorf("upsert applicationsignals services: %w", uerr)
			}
			total += len(batch)
			inserted += n
			services = append(services, instrumented...)
		}
	}
	return total, inserted, services, nil
}

// isAppSignalsInstrumentationSkip reports a service whose configurations
// cannot be listed for a reason that is not a failure: ResourceNotFound (the
// op's modeled not-found, e.g. a service gone since ListServices), or an op
// this region's endpoint does not serve.
func isAppSignalsInstrumentationSkip(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException") || isOpNotDeployed(err)
}

// scanApplicationSignalsInstrumentationConfigurations stores the probe
// instrumentation configurations of each service and links each under its
// service. The op has no list-all form: it needs a service name and
// environment, and services holds only the services stored this scan, i.e.
// those ListServices reported with telemetry in the trailing 24h window. A
// probe of a service that sent no telemetry in that window is not listed and
// no warning says so. Services sharing a name and environment (their other
// KeyAttributes differ) are queried once and their configurations linked under
// the first. Per service: a skip per isAppSignalsInstrumentationSkip is
// silent, a denial warns once, and a transient error after the SDK's retries
// or a ValidationException skips that service with one warning for all such
// services; anything else fails the phase.
func scanApplicationSignalsInstrumentationConfigurations(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string, services []applicationSignalsService) (int, int, error) {
	type nameEnv struct{ name, environment string }
	seen := make(map[nameEnv]bool, len(services))
	byNativeID := make(map[string]applicationSignalsService, len(services))
	parents := make([]childParent, 0, len(services))
	for _, s := range services {
		key := nameEnv{s.name, s.environment}
		if seen[key] {
			continue
		}
		seen[key] = true
		byNativeID[s.nativeID] = s
		parents = append(parents, childParent{id: s.name + " in " + s.environment, arn: s.nativeID})
	}
	return childFanOutSkipNotReady(ctx, st, acct, region, appSignalsListInstrumentationConfigurationsOp, parents,
		isAppSignalsInstrumentationSkip, byParentID, "service(s)",
		"a service name or environment ListServices reports may not match the instrumentation API's input pattern",
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return listAppSignalsInstrumentationConfigurations(ctx, client, acct, region, scanID, byNativeID[p.arn])
		})
}

// listAppSignalsInstrumentationConfigurations lists one service's probe
// configurations; NativeID is the returned ARN. Breakpoints are not listed:
// they expire within 24 hours (InstrumentationTypeBreakpoint), so they are not
// durable.
func listAppSignalsInstrumentationConfigurations(ctx context.Context, client applicationSignalsAPI, acct *account, region, scanID string, svc applicationSignalsService) ([]*store.Resource, error) {
	pager := applicationsignals.NewListInstrumentationConfigurationsPaginator(client,
		&applicationsignals.ListInstrumentationConfigurationsInput{
			Service: &svc.name, Environment: &svc.environment,
			InstrumentationType: applicationsignalstypes.InstrumentationTypeProbe,
		},
		func(o *applicationsignals.ListInstrumentationConfigurationsPaginatorOptions) {
			o.Limit = appSignalsInstrumentationPageLimit
			o.StopOnDuplicateToken = true
		})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range out.LatestConfigurations {
			arn := sv(c.ARN)
			if arn == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeApplicationSignalsInstrumentationConfiguration, NativeID: arn,
				Name: nonEmptyPtr(sv(c.Description)), Region: &region, CreatedAt: tp(c.CreatedAt),
				AttributesJSON: mustJSON(flattenAppSignalsInstrumentationConfiguration(c)), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

// appSignalsInstrumentationConfigurationAttrs shadows the Location and
// CaptureConfiguration unions, which marshal as an unlabelled {"Value": ...},
// with one key per known member so the stored row names the member kind. A
// member newer than the pinned SDK is kept under UnknownMember with its tag and
// its value, which is []byte and so stored base64-encoded.
type appSignalsInstrumentationConfigurationAttrs struct {
	applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv
	Location             appSignalsLocationAttrs
	CaptureConfiguration appSignalsCaptureConfigurationAttrs
}

type appSignalsLocationAttrs struct {
	CodeLocation  *applicationsignalstypes.CodeLocation       `json:",omitempty"`
	UnknownMember *applicationsignalstypes.UnknownUnionMember `json:",omitempty"`
}

type appSignalsCaptureConfigurationAttrs struct {
	CodeCapture   *applicationsignalstypes.CodeCaptureConfiguration `json:",omitempty"`
	UnknownMember *applicationsignalstypes.UnknownUnionMember       `json:",omitempty"`
}

func flattenAppSignalsInstrumentationConfiguration(c applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv) appSignalsInstrumentationConfigurationAttrs {
	out := appSignalsInstrumentationConfigurationAttrs{InstrumentationConfigurationWithoutServiceEnv: c}
	switch l := c.Location.(type) {
	case *applicationsignalstypes.LocationMemberCodeLocation:
		out.Location.CodeLocation = &l.Value
	case *applicationsignalstypes.UnknownUnionMember:
		out.Location.UnknownMember = l
	}
	switch cc := c.CaptureConfiguration.(type) {
	case *applicationsignalstypes.CaptureConfigurationMemberCodeCapture:
		out.CaptureConfiguration.CodeCapture = &cc.Value
	case *applicationsignalstypes.UnknownUnionMember:
		out.CaptureConfiguration.UnknownMember = cc
	}
	return out
}

// scanApplicationSignalsGroupingConfiguration upserts the singleton
// grouping-configuration resource by aggregating ListGroupingAttributeDefinitions
// pages into one attrs body. Emitted only when the API returns at least one
// definition or a non-zero UpdatedAt; empty means no configuration exists in
// this region.
func scanApplicationSignalsGroupingConfiguration(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	type aggregate struct {
		GroupingAttributeDefinitions []applicationsignalsListGroupingAttributeDefinitionsItem `json:"GroupingAttributeDefinitions"`
		UpdatedAt                    *time.Time                                               `json:"UpdatedAt,omitempty"`
	}
	var agg aggregate
	var nextToken *string
	for {
		out, lerr := client.ListGroupingAttributeDefinitions(ctx, &applicationsignals.ListGroupingAttributeDefinitionsInput{NextToken: nextToken})
		if lerr != nil {
			if isAccessDenied(lerr) {
				return total, inserted, skipIfAccessDenied(st, "applicationsignals:ListGroupingAttributeDefinitions", acct.ID, region, lerr)
			}
			return total, inserted, fmt.Errorf("applicationsignals:ListGroupingAttributeDefinitions: %w", lerr)
		}
		for _, d := range out.GroupingAttributeDefinitions {
			agg.GroupingAttributeDefinitions = append(agg.GroupingAttributeDefinitions, applicationsignalsListGroupingAttributeDefinitionsItem{
				GroupingName:         sv(d.GroupingName),
				DefaultGroupingValue: sv(d.DefaultGroupingValue),
				GroupingSourceKeys:   d.GroupingSourceKeys,
			})
		}
		if out.UpdatedAt != nil {
			agg.UpdatedAt = out.UpdatedAt
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	// No configuration created in this region — skip the singleton row.
	if len(agg.GroupingAttributeDefinitions) == 0 && agg.UpdatedAt == nil {
		return 0, 0, nil
	}
	nativeID := applicationSignalsGroupingConfigurationNativeID(region, acct.ID)
	name := "grouping-configuration"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeApplicationSignalsGroupingConfiguration,
		NativeID:       nativeID,
		Name:           &name,
		Region:         &region,
		CreatedAt:      tp(agg.UpdatedAt),
		AttributesJSON: mustJSON(agg),
		DiscoveredBy:   scanID,
	}
	n, err := st.UpsertResources([]*store.Resource{r})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert applicationsignals grouping-configuration: %w", err)
	}
	return 1, n, nil
}

// applicationsignalsListGroupingAttributeDefinitionsItem is a JSON-friendly
// projection of `applicationsignalstypes.GroupingAttributeDefinition` — the
// SDK type's unexported smithy serde marker breaks `json.Marshal` when
// nested in the aggregate. Captures only the fields graph analysis needs.
type applicationsignalsListGroupingAttributeDefinitionsItem struct {
	GroupingName         string   `json:"GroupingName"`
	DefaultGroupingValue string   `json:"DefaultGroupingValue,omitempty"`
	GroupingSourceKeys   []string `json:"GroupingSourceKeys,omitempty"`
}

func scanApplicationSignalsSLOs(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := applicationsignals.NewListServiceLevelObjectivesPaginator(client, &applicationsignals.ListServiceLevelObjectivesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "applicationsignals:ListServiceLevelObjectives", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("applicationsignals:ListServiceLevelObjectives: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.SloSummaries))
		for _, s := range page.SloSummaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			name := sv(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeApplicationSignalsSLO,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(s.CreatedTime),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert applicationsignals slos: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
