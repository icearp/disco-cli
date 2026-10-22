package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
// ResourcePolicy synth ARN: {targetArn}/policy. Every phase runs whatever the
// others returned, and the policies are read for the targets listed before
// any listing failed; the counts are every row stored and the errors join
// through phaseErrs. eventDataStores are the stores
// scanCloudTrailEventDataStores stored.
func scanCloudTrailExtended(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, eventDataStores []childParent) (total, inserted int, err error) {
	var errs phaseErrs
	channelArns, t, i, err := scanCTChannels(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	errs.add(ctListChannelsOp, err)

	dashboardArns, t, i, err := scanCTDashboards(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	errs.add(ctListDashboardsOp, err)

	policyTargets := slices.Concat(channelArns, dashboardArns)
	for _, eds := range eventDataStores {
		policyTargets = append(policyTargets, eds.arn)
	}
	t, i, err = scanCTResourcePolicies(ctx, client, policyTargets, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	errs.add("cloudtrail:GetResourcePolicy", err)
	return total, inserted, errs.join(st, acct, region)
}

const (
	ctListChannelsOp   = "cloudtrail:ListChannels"
	ctListDashboardsOp = "cloudtrail:ListDashboards"
)

// ctListPages reads a hand-rolled NextToken listing, page by page, until list
// fails or returns its last page. A denial warns under op and ends the listing
// without an error; any other error, and a repeated token, ends it with one.
// ListDashboards has no SDK paginator, and ListChannels' would loop on a
// repeated token or, told to stop on one, read as a complete listing.
func ctListPages(st *store.Store, acct *account, region, op string, list func(token *string) (*string, error)) error {
	var token *string
	for {
		next, err := list(token)
		if isAccessDenied(err) {
			return skipIfAccessDenied(st, op, acct.ID, region, err)
		}
		if err != nil {
			return err
		}
		done, err := lastPage(token, next)
		if done {
			return err
		}
		token = next
	}
}

// ctDetailFailures tallies the Lake items whose detail read (GetChannel,
// GetDashboard) failed, so that each is stored as its list element.
type ctDetailFailures struct {
	denied    skipTally
	transient transientTally
	other     skipTally
}

func (f *ctDetailFailures) add(key string, err error) {
	switch {
	case isAccessDenied(err):
		f.denied.skipped(key, err)
	case f.transient.drop(key, err):
	default:
		f.other.skipped(key, err)
	}
}

// settle warns once per cause about the denied and transient reads and
// returns the other failed reads as one error joined to err, the listing's.
// The warnings say the items were stored, so they are not given after a
// failed write.
func (f *ctDetailFailures) settle(st *store.Store, acct *account, region, listOp, op string, err error) error {
	kept := "%d item(s) stored as their " + listOp + " element, without the " + op + " body, first %s: %w"
	if !errors.Is(err, store.ErrStoreWrite) {
		if f.denied.count > 0 {
			_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf(kept, f.denied.count, f.denied.first, f.denied.err))
		}
		if f.transient.count > 0 {
			_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf(kept, f.transient.count, f.transient.first, f.transient.err))
		}
	}
	if f.other.count == 0 {
		return err
	}
	return errors.Join(err, fmt.Errorf("%s: "+kept, op, f.other.count, f.other.first, f.other.err))
}

// scanCTChannels stores the Lake channels and returns the ARNs of those
// listed. The pages read before a failed listing are stored. A channel
// GetChannel no longer finds (ChannelNotFoundException, in the op's model) was
// deleted since it was listed and is not stored; any other GetChannel failure
// stores it as its list element, per ctDetailFailures.
func scanCTChannels(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		batch   []*store.Resource
		arns    []string
		arnless skipTally
		failed  ctDetailFailures
	)
	listErr := ctListPages(st, acct, region, ctListChannelsOp, func(token *string) (*string, error) {
		out, err := client.ListChannels(ctx, &cloudtrail.ListChannelsInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, c := range out.Channels {
			arn := sv(c.ChannelArn)
			if arn == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(c.Name)), nil)
				continue
			}
			detail, dErr := client.GetChannel(ctx, &cloudtrail.GetChannelInput{Channel: &arn})
			if isAPIErrorCode(dErr, "ChannelNotFoundException") {
				continue
			}
			var attrs any = detail
			if dErr != nil {
				failed.add(arn, dErr)
				attrs = c
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailChannel, NativeID: arn,
				Name: c.Name, Region: &region,
				ManagedByProvider: strings.HasPrefix(sv(c.Name), "aws-service-channel/"),
				AttributesJSON:    mustJSON(attrs), DiscoveredBy: scanID,
			})
		}
		return out.NextToken, nil
	})
	warnNotStored(st, ctListChannelsOp, acct, region, &arnless, "channels have no ChannelArn")
	t, i, err := upsertListed(st, batch, ctListChannelsOp, listErr)
	return arns, t, i, failed.settle(st, acct, region, ctListChannelsOp, "cloudtrail:GetChannel", err)
}

// scanCTDashboards stores the Lake dashboards and returns the ARNs of those
// listed. The pages read before a failed listing are stored. A dashboard
// GetDashboard no longer finds (ResourceNotFoundException, in the op's model)
// was deleted since it was listed and is not stored; any other GetDashboard
// failure stores it as its list element, per ctDetailFailures.
func scanCTDashboards(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		batch   []*store.Resource
		arns    []string
		arnless skipTally
		failed  ctDetailFailures
	)
	listErr := ctListPages(st, acct, region, ctListDashboardsOp, func(token *string) (*string, error) {
		out, err := client.ListDashboards(ctx, &cloudtrail.ListDashboardsInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, d := range out.Dashboards {
			arn := sv(d.DashboardArn)
			if arn == "" {
				arnless.skipped(fmt.Sprintf("of type %s", d.Type), nil)
				continue
			}
			detail, dErr := client.GetDashboard(ctx, &cloudtrail.GetDashboardInput{DashboardId: &arn})
			if isAPIErrorCode(dErr, "ResourceNotFoundException") {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailDashboard, NativeID: arn,
				Region:         &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}
			if dErr != nil {
				failed.add(arn, dErr)
			} else {
				r.Status, r.AttributesJSON = nonEmptyPtr(string(detail.Status)), mustJSON(detail)
			}
			arns = append(arns, arn)
			batch = append(batch, r)
		}
		return out.NextToken, nil
	})
	warnNotStored(st, ctListDashboardsOp, acct, region, &arnless, "dashboards have no DashboardArn")
	t, i, err := upsertListed(st, batch, ctListDashboardsOp, listErr)
	return arns, t, i, failed.settle(st, acct, region, ctListDashboardsOp, "cloudtrail:GetDashboard", err)
}

// scanCTResourcePolicies stores the resource-based policy of each target that
// has one. A target with none, or that is gone or of a type without policies,
// is skipped silently; denied reads, and reads that still fail transiently
// after the SDK's retries, are skipped with one warning per cause. Any other
// error ends the reads and is returned after the policies read before it are
// stored.
func scanCTResourcePolicies(ctx context.Context, client cloudtrailAPI, targets []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "cloudtrail:GetResourcePolicy"
	var (
		batch     []*store.Resource
		denied    skipTally
		transient transientTally
		getErr    error
	)
	for _, target := range targets {
		out, err := client.GetResourcePolicy(ctx, &cloudtrail.GetResourcePolicyInput{ResourceArn: &target})
		if isAccessDenied(err) {
			denied.skipped(target, err)
			continue
		}
		if transient.drop(target, err) {
			continue
		}
		if isAPIErrorCode(err, "ResourcePolicyNotFoundException", "ResourceNotFoundException", "ResourceTypeNotSupportedException") {
			continue
		}
		if err != nil {
			getErr = fmt.Errorf("%s: %w", target, err)
			break
		}
		if sv(out.ResourcePolicy) == "" && sv(out.DelegatedAdminResourcePolicy) == "" {
			continue
		}
		label := target
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCloudTrailResourcePolicy, NativeID: target + "/policy",
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d resource policy read(s) denied, first for %s: %w", denied.count, denied.first, denied.err))
	}
	transient.warnSkipped(st, op, acct, region)
	return upsertListed(st, batch, op, getErr)
}
