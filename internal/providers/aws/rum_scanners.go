package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/rum"
	rumtypes "github.com/aws/aws-sdk-go-v2/service/rum/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRUMAppMonitor, Service: "rum"})
	registerType(restype.Descriptor{Type: TypeRUMMetricsDestination, Service: "rum"})
	registerType(restype.Descriptor{Type: TypeRUMMetricDefinition, Service: "rum"})
	registerService(serviceEntry{
		name: "aws:rum",
		fn:   scanRUM,
	})
}

const (
	rumListAppMonitorsOp                 = "rum:ListAppMonitors"
	rumListMetricsDestinationsOp         = "rum:ListRumMetricsDestinations"
	rumBatchGetMetricDefinitionsOp       = "rum:BatchGetRumMetricDefinitions"
	rumPageLimit                   int32 = 100 // model cap (MaxResultsInteger 1..100) for all three ops
)

// scanRUM discovers CloudWatch RUM app monitors, then the extended-metrics
// destinations of every app monitor it stored, then the metric definitions of
// every destination it stored. The child phases run whatever the app monitor
// phase returned, so a child failure costs the app monitors nothing. The app
// monitor phase's transient error is demoted to a warning, since one transient
// member would make the dispatcher read the whole join as transient; the
// counts are summed.
func scanRUM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := rum.NewFromConfig(acct.cfg, func(o *rum.Options) { o.Region = region })
	total, inserted, monitors, err := scanRUMAppMonitors(ctx, client, acct, region, st, scanID)
	t, i, dests, derr := scanRUMMetricsDestinations(ctx, client, acct, region, st, scanID, monitors)
	total, inserted = total+t, inserted+i
	t, i, merr := scanRUMMetricDefinitions(ctx, client, acct, region, st, scanID, dests)
	return total + t, inserted + i, errors.Join(demoteTransient(st, rumListAppMonitorsOp, acct, region, err), derr, merr)
}

// rumAppMonitorARN is the app monitor ARN (Service Reference format
// appmonitor/${Name}); ListAppMonitors returns no ARN.
func rumAppMonitorARN(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:rum:%s:%s:appmonitor/%s", region, acctID, name)
}

// scanRUMAppMonitors stores the app monitors a page at a time and returns the
// ones stored before any failure, keyed by name. A denial on a later page
// warns and keeps the earlier pages.
func scanRUMAppMonitors(ctx context.Context, client rum.ListAppMonitorsAPIClient, acct *account, region string, st *store.Store, scanID string) (total, inserted int, monitors []childParent, err error) {
	pager := rum.NewListAppMonitorsPaginator(client, &rum.ListAppMonitorsInput{}, func(o *rum.ListAppMonitorsPaginatorOptions) {
		o.Limit = rumPageLimit
		o.StopOnDuplicateToken = true
	})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, monitors, skipIfAccessDenied(st, rumListAppMonitorsOp, acct.ID, region, err)
			}
			return total, inserted, monitors, fmt.Errorf("%s: %w", rumListAppMonitorsOp, err)
		}
		var (
			batch []*store.Resource
			page  []childParent
		)
		for _, m := range out.AppMonitorSummaries {
			name := sv(m.Name)
			if name == "" {
				continue
			}
			arn := rumAppMonitorARN(region, acct.ID, name)
			status := string(m.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRUMAppMonitor, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
			page = append(page, childParent{id: name, arn: arn})
		}
		t, i, err := upsertBatch(st, batch, "rum app-monitors")
		if err != nil {
			return total, inserted, monitors, err
		}
		total += t
		inserted += i
		monitors = append(monitors, page...)
	}
	return total, inserted, monitors, nil
}

// rumMetricsDestinationNativeID keys a destination under its app monitor: the
// API returns no destination ARN, and the Service Reference defines ARNs only
// for app monitors. An app monitor has at most one destination per
// (Destination, DestinationArn). The enum goes first; DestinationArn is
// free-form (it holds "/") and goes last, omitted when empty, as it is for
// CloudWatch.
func rumMetricsDestinationNativeID(monitorARN string, dest rumtypes.MetricDestinationSummary) string {
	id := monitorARN + "/metrics-destination/" + string(dest.Destination)
	if arn := sv(dest.DestinationArn); arn != "" {
		id += "/" + arn
	}
	return id
}

// rumMetricDefinitionNativeID keys a metric definition under its destination;
// the API returns no definition ARN.
func rumMetricDefinitionNativeID(destNativeID, metricDefinitionID string) string {
	return destNativeID + "/metric-definition/" + metricDefinitionID
}

// isRUMParentGone reports whether err says the app monitor or destination no
// longer exists, i.e. it was deleted after its listing.
func isRUMParentGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// rumChildFanOut lists each parent's children. Per parent: ResourceNotFound
// (deleted since listing) is skipped silently, a denial warns once, a
// transient error after the SDK's retries and a ValidationException are each
// skipped with one warning for all such parents, and anything else fails the
// phase; why says in the warning why the service may refuse a parent. How
// these ops answer for a parent being deleted has not been observed; failing
// the phase would fail it on every scan, and skipping silently would hide a
// real input error.
func rumChildFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op, why string, parents []childParent,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	return childFanOutSkipNotReady(ctx, st, acct, region, op, parents, isRUMParentGone, byParentID,
		"parent(s)", why, list)
}

// rumDestination is a metrics destination listed under an app monitor, with
// the inputs BatchGetRumMetricDefinitions keys on.
type rumDestination struct {
	monitorName string
	nativeID    string
	summary     rumtypes.MetricDestinationSummary
}

// scanRUMMetricsDestinations stores each app monitor's extended-metrics
// destinations, links each under its app monitor, and returns them. monitors
// holds only app monitors stored this scan; the returned destinations are only
// those stored, none when the phase fails.
func scanRUMMetricsDestinations(ctx context.Context, client rum.ListRumMetricsDestinationsAPIClient, acct *account, region string, st *store.Store, scanID string, monitors []childParent) (int, int, []rumDestination, error) {
	var (
		mu    sync.Mutex
		dests []rumDestination
	)
	total, inserted, err := rumChildFanOut(ctx, st, acct, region, rumListMetricsDestinationsOp,
		"the app monitor may be being deleted", monitors,
		func(ctx context.Context, m childParent) ([]*store.Resource, error) {
			rows, listed, err := listRUMMetricsDestinations(ctx, client, acct, region, scanID, m)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			dests = append(dests, listed...)
			mu.Unlock()
			return rows, nil
		})
	if err != nil {
		return total, inserted, nil, err
	}
	return total, inserted, dests, nil
}

func listRUMMetricsDestinations(ctx context.Context, client rum.ListRumMetricsDestinationsAPIClient, acct *account, region, scanID string, m childParent) ([]*store.Resource, []rumDestination, error) {
	pager := rum.NewListRumMetricsDestinationsPaginator(client,
		&rum.ListRumMetricsDestinationsInput{AppMonitorName: &m.id},
		func(o *rum.ListRumMetricsDestinationsPaginatorOptions) {
			o.Limit = rumPageLimit
			o.StopOnDuplicateToken = true
		})
	var (
		rows  []*store.Resource
		dests []rumDestination
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, d := range out.Destinations {
			if d.Destination == "" {
				continue
			}
			nativeID := rumMetricsDestinationNativeID(m.arn, d)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRUMMetricsDestination, NativeID: nativeID,
				Name: nonEmptyPtr(string(d.Destination)), Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
			dests = append(dests, rumDestination{monitorName: m.id, nativeID: nativeID, summary: d})
		}
	}
	return rows, dests, nil
}

// scanRUMMetricDefinitions stores each destination's metric definitions and
// links each under its destination. dests holds only destinations stored this
// scan.
func scanRUMMetricDefinitions(ctx context.Context, client rum.BatchGetRumMetricDefinitionsAPIClient, acct *account, region string, st *store.Store, scanID string, dests []rumDestination) (int, int, error) {
	byID := make(map[string]rumDestination, len(dests))
	parents := make([]childParent, len(dests))
	for i, d := range dests {
		byID[d.nativeID] = d
		parents[i] = childParent{id: d.nativeID, arn: d.nativeID}
	}
	return rumChildFanOut(ctx, st, acct, region, rumBatchGetMetricDefinitionsOp,
		"the destination or its app monitor may be being deleted", parents,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return listRUMMetricDefinitions(ctx, client, acct, region, scanID, byID[p.id])
		})
}

// listRUMMetricDefinitions lists one destination's metric definitions.
// DestinationArn is sent only for Evidently: the API requires it there and
// documents that it must not be sent for CloudWatch.
func listRUMMetricDefinitions(ctx context.Context, client rum.BatchGetRumMetricDefinitionsAPIClient, acct *account, region, scanID string, d rumDestination) ([]*store.Resource, error) {
	in := &rum.BatchGetRumMetricDefinitionsInput{AppMonitorName: &d.monitorName, Destination: d.summary.Destination}
	if d.summary.Destination == rumtypes.MetricDestinationEvidently {
		in.DestinationArn = d.summary.DestinationArn
	}
	pager := rum.NewBatchGetRumMetricDefinitionsPaginator(client, in,
		func(o *rum.BatchGetRumMetricDefinitionsPaginatorOptions) {
			o.Limit = rumPageLimit
			o.StopOnDuplicateToken = true
		})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, md := range out.MetricDefinitions {
			id := sv(md.MetricDefinitionId)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRUMMetricDefinition, NativeID: rumMetricDefinitionNativeID(d.nativeID, id),
				Name: md.Name, Region: &region,
				AttributesJSON: mustJSON(md), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
