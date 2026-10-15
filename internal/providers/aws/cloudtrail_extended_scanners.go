package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/icearp/disco-cli/store"
)

const (
	cloudTrailInsightSelectorsOp   = "cloudtrail:GetInsightSelectors"
	cloudTrailEventConfigurationOp = "cloudtrail:GetEventConfiguration"
)

// scanCloudTrailInsightSelectors stores the Insights selectors of each trail
// and event data store that has Insights enabled, one row per parent with
// NativeID {parentARN}/insight-selector. InsightNotEnabledException is how the
// op answers a parent without Insights, so it stores nothing. The op does not
// model EventDataStoreNotFoundException, but a store deleted since listing is
// a vanished parent like a deleted trail, so it is skipped the same way.
func scanCloudTrailInsightSelectors(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, parents cloudTrailParents) (int, int, error) {
	return cloudTrailConfigFanOut(ctx, st, acct, region, cloudTrailInsightSelectorsOp, parents.insightTrails, parents.insightEventDataStores,
		[]string{"InsightNotEnabledException", "TrailNotFoundException", "EventDataStoreNotFoundException"},
		func(ctx context.Context, p childParent, trail bool) (*store.Resource, error) {
			in := &cloudtrail.GetInsightSelectorsInput{EventDataStore: &p.arn}
			if trail {
				in = &cloudtrail.GetInsightSelectorsInput{TrailName: &p.arn}
			}
			return getCloudTrailInsightSelectors(ctx, client, in, p, acct, region, scanID)
		})
}

func getCloudTrailInsightSelectors(ctx context.Context, client cloudtrailAPI, in *cloudtrail.GetInsightSelectorsInput, p childParent, acct *account, region, scanID string) (*store.Resource, error) {
	out, err := client.GetInsightSelectors(ctx, in)
	if err != nil || len(out.InsightSelectors) == 0 {
		return nil, err
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCloudTrailInsightSelector, NativeID: p.arn + "/insight-selector",
		Name: nonEmptyPtr(p.id), Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}, nil
}

// scanCloudTrailEventConfigurations stores the event configuration of each
// trail and event data store that holds one, one row per parent with NativeID
// {parentARN}/event-configuration. A store whose category the op does not
// serve, or whose status (pending deletion) it refuses, is an expected state
// no IAM change fixes, so it is skipped silently like a vanished parent.
func scanCloudTrailEventConfigurations(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, parents cloudTrailParents) (int, int, error) {
	return cloudTrailConfigFanOut(ctx, st, acct, region, cloudTrailEventConfigurationOp, parents.trails, parents.eventDataStores,
		[]string{
			"EventDataStoreNotFoundException", "TrailNotFoundException",
			"InvalidEventDataStoreCategoryException", "InvalidEventDataStoreStatusException",
		},
		func(ctx context.Context, p childParent, trail bool) (*store.Resource, error) {
			in := &cloudtrail.GetEventConfigurationInput{EventDataStore: &p.arn}
			if trail {
				in = &cloudtrail.GetEventConfigurationInput{TrailName: &p.id}
			}
			return getCloudTrailEventConfiguration(ctx, client, in, p, acct, region, scanID)
		})
}

// getCloudTrailEventConfiguration returns nil for a parent that holds no
// configuration: no aggregation, no context key selectors and the Standard
// event size. Standard is the assumed default (the model documents none);
// PutEventConfiguration must raise it to Large before it accepts context key
// selectors.
func getCloudTrailEventConfiguration(ctx context.Context, client cloudtrailAPI, in *cloudtrail.GetEventConfigurationInput, p childParent, acct *account, region, scanID string) (*store.Resource, error) {
	out, err := client.GetEventConfiguration(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(out.AggregationConfigurations) == 0 && len(out.ContextKeySelectors) == 0 && out.MaxEventSize != cloudtrailtypes.MaxEventSizeLarge {
		return nil, nil
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCloudTrailEventConfiguration, NativeID: p.arn + "/event-configuration",
		Name: nonEmptyPtr(p.id), Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}, nil
}

// cloudTrailRefusalRemedies maps each modeled per-parent refusal of the
// configuration ops, none observed live, to what the reader can do about it.
var cloudTrailRefusalRemedies = map[string]string{
	"NoManagementAccountSLRExistsException": "create the CloudTrail service-linked role (AWSServiceRoleForCloudTrail) in the organization management account",
	"OperationNotPermittedException":        "CloudTrail does not permit this operation on this parent from the scanned account; if the parent is managed by another account, scan that account",
	"UnsupportedOperationException":         "CloudTrail does not support this operation for this parent; nothing to fix unless this configuration was expected",
}

// warnCloudTrailParentRefused records that op was refused for parent with a
// modeled per-parent refusal code, so that parent's configuration is missing
// from this scan. Safe from fan-out goroutines: ReportWarning only calls
// OnWarn, which the scan runner guards with a mutex.
func warnCloudTrailParentRefused(st *store.Store, op string, acct *account, region, parent, code string, err error) {
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  op,
		Scope:    acct.ID + "/" + region,
		Message: fmt.Sprintf("%s refused for %s with %s; its configuration is not recorded this scan. To fix: %s (cause: %v)",
			op, parent, code, cloudTrailRefusalRemedies[code], err),
	})
}

// cloudTrailConfigFanOut reads one configuration per trail and per event data
// store through get (trail tells it which input to build) and links each row
// under its parent. Per parent: a goneCodes error (deleted since listing, or
// the op's "not configured" or an expected-state answer) is skipped silently;
// an AccessDenied is skipped with one access-denied warning for op; a
// cloudTrailRefusalRemedies code is skipped with one warning per code naming
// the parent and the remedy; a transient error after the SDK's retries is
// skipped and all such skips share one warning; anything else fails the
// fan-out. The trail and event-data-store fan-outs run independently and
// their errors are joined.
//
// The denial warning is raised inside isSkip, not by childFanOut's keyErr
// (whose doc says isSkip errors drop silently): keyErr's deny-once lives per
// childFanOut call, and one Once shared across both runs keeps a denial to
// one warning per op. keyErr's own AccessDenied branch is unreachable here.
func cloudTrailConfigFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, trails, eventDataStores []childParent,
	goneCodes []string, get func(ctx context.Context, p childParent, trail bool) (*store.Resource, error),
) (int, int, error) {
	var (
		transient transientTally
		denyOnce  sync.Once
	)
	refusalOnce := make(map[string]*sync.Once, len(cloudTrailRefusalRemedies))
	for code := range cloudTrailRefusalRemedies {
		refusalOnce[code] = new(sync.Once)
	}
	isSkip := func(err error) bool {
		if isAPIErrorCode(err, goneCodes...) {
			return true
		}
		if isAccessDenied(err) {
			denyOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
			return true
		}
		return false
	}
	// refused reports whether err is a per-parent refusal, warning once per code.
	refused := func(p childParent, err error) bool {
		for code, once := range refusalOnce {
			if isAPIErrorCode(err, code) {
				once.Do(func() { warnCloudTrailParentRefused(st, op, acct, region, p.arn, code, err) })
				return true
			}
		}
		return false
	}
	run := func(parents []childParent, trail bool) (int, int, error) {
		return childFanOut(ctx, st, acct, region, op, parents, isSkip, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			r, err := get(ctx, p, trail)
			switch {
			case isTransientNetworkError(err):
				transient.skipped(p.arn, err)
				return nil, nil
			case refused(p, err):
				return nil, nil
			case err != nil || r == nil:
				return nil, err
			default:
				return []*store.Resource{r}, nil
			}
		})
	}
	t1, i1, err1 := run(trails, true)
	t2, i2, err2 := run(eventDataStores, false)
	transient.warnSkipped(st, op, acct, region)
	return t1 + t2, i1 + i2, errors.Join(err1, err2)
}

// scanCloudTrailExtended discovers Lake channels, dashboards, and
// resource-based policies attached to channels/dashboards/event-data-stores.
// ResourcePolicy synth ARN: {targetArn}/policy.
func scanCloudTrailExtended(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	channelArns, t, i, ferr := scanCTChannels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	dashboardArns, t, i, ferr := scanCTDashboards(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	edsArns, ferr := scanCTEDSARNs(ctx, client, acct, region, st)
	if ferr != nil {
		return total, inserted, ferr
	}

	policyTargets := append(append(channelArns, dashboardArns...), edsArns...)
	t, i, ferr = scanCTResourcePolicies(ctx, client, policyTargets, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanCTChannels(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListChannels(ctx, &cloudtrail.ListChannelsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "cloudtrail:ListChannels", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("cloudtrail:ListChannels: %w", err)
		}
		for _, c := range out.Channels {
			arn := sv(c.ChannelArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			detail, dErr := client.GetChannel(ctx, &cloudtrail.GetChannelInput{Channel: &arn})
			var attrs any = c
			if dErr == nil {
				attrs = detail
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailChannel, NativeID: arn,
				Name: c.Name, Region: &region,
				ManagedByProvider: strings.HasPrefix(sv(c.Name), "aws-service-channel/"),
				AttributesJSON:    mustJSON(attrs), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "cloudtrail channels")
	return arns, t, i, err
}

func scanCTDashboards(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListDashboards(ctx, &cloudtrail.ListDashboardsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "cloudtrail:ListDashboards", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("cloudtrail:ListDashboards: %w", err)
		}
		for _, d := range out.Dashboards {
			arn := sv(d.DashboardArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			detail, dErr := client.GetDashboard(ctx, &cloudtrail.GetDashboardInput{DashboardId: &arn})
			var attrs any = d
			var status string
			if dErr == nil {
				attrs = detail
				status = string(detail.Status)
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailDashboard, NativeID: arn,
				Region:         &region,
				AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
			}
			if status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "cloudtrail dashboards")
	return arns, t, i, err
}

// scanCTEDSARNs collects scanned event-data-store ARNs (already upserted by
// the main scanner) for resource-policy fan-out.
func scanCTEDSARNs(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store) ([]string, error) {
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListEventDataStores(ctx, &cloudtrail.ListEventDataStoresInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("cloudtrail:ListEventDataStores(policy): %w", err)
		}
		for _, e := range out.EventDataStores {
			if a := sv(e.EventDataStoreArn); a != "" {
				arns = append(arns, a)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return arns, nil
}

func scanCTResourcePolicies(ctx context.Context, client cloudtrailAPI, targets []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, target := range targets {
		t := target
		out, err := client.GetResourcePolicy(ctx, &cloudtrail.GetResourcePolicyInput{ResourceArn: &t})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourcePolicyNotFoundException", "ResourceNotFoundException", "ResourceTypeNotSupportedException") {
				continue
			}
			return 0, 0, fmt.Errorf("cloudtrail:GetResourcePolicy %s: %w", t, err)
		}
		if sv(out.ResourcePolicy) == "" && sv(out.DelegatedAdminResourcePolicy) == "" {
			continue
		}
		arn := t + "/policy"
		label := t
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCloudTrailResourcePolicy, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cloudtrail resource-policies")
}
