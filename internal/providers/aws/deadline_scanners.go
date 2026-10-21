package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/deadline"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDeadlineFarm, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineBudget, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineVolume, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineFleet, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineLicenseEndpoint, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineLimit, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineMeteredProduct, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineMonitor, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueue, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueEnvironment, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueFleetAssociation, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueLimitAssociation, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineStorageProfile, Service: "deadline"})
	registerService(serviceEntry{
		name: "aws:deadline",
		fn:   scanDeadline,
	})
}

type deadlineAPI interface {
	ListFarms(context.Context, *deadline.ListFarmsInput, ...func(*deadline.Options)) (*deadline.ListFarmsOutput, error)
	ListFleets(context.Context, *deadline.ListFleetsInput, ...func(*deadline.Options)) (*deadline.ListFleetsOutput, error)
	ListLicenseEndpoints(context.Context, *deadline.ListLicenseEndpointsInput, ...func(*deadline.Options)) (*deadline.ListLicenseEndpointsOutput, error)
	ListLimits(context.Context, *deadline.ListLimitsInput, ...func(*deadline.Options)) (*deadline.ListLimitsOutput, error)
	ListFarmMembers(context.Context, *deadline.ListFarmMembersInput, ...func(*deadline.Options)) (*deadline.ListFarmMembersOutput, error)
	ListFleetMembers(context.Context, *deadline.ListFleetMembersInput, ...func(*deadline.Options)) (*deadline.ListFleetMembersOutput, error)
	ListQueueMembers(context.Context, *deadline.ListQueueMembersInput, ...func(*deadline.Options)) (*deadline.ListQueueMembersOutput, error)
	ListMeteredProducts(context.Context, *deadline.ListMeteredProductsInput, ...func(*deadline.Options)) (*deadline.ListMeteredProductsOutput, error)
	ListMonitors(context.Context, *deadline.ListMonitorsInput, ...func(*deadline.Options)) (*deadline.ListMonitorsOutput, error)
	ListQueues(context.Context, *deadline.ListQueuesInput, ...func(*deadline.Options)) (*deadline.ListQueuesOutput, error)
	ListQueueEnvironments(context.Context, *deadline.ListQueueEnvironmentsInput, ...func(*deadline.Options)) (*deadline.ListQueueEnvironmentsOutput, error)
	ListQueueFleetAssociations(context.Context, *deadline.ListQueueFleetAssociationsInput, ...func(*deadline.Options)) (*deadline.ListQueueFleetAssociationsOutput, error)
	ListQueueLimitAssociations(context.Context, *deadline.ListQueueLimitAssociationsInput, ...func(*deadline.Options)) (*deadline.ListQueueLimitAssociationsOutput, error)
	ListStorageProfiles(context.Context, *deadline.ListStorageProfilesInput, ...func(*deadline.Options)) (*deadline.ListStorageProfilesOutput, error)
	ListBudgets(context.Context, *deadline.ListBudgetsInput, ...func(*deadline.Options)) (*deadline.ListBudgetsOutput, error)
	ListVolumes(context.Context, *deadline.ListVolumesInput, ...func(*deadline.Options)) (*deadline.ListVolumesOutput, error)
}

// dlFarmRef carries (farmId, farmARN) and the farm's stored queues and fleets
// across phases.
type dlFarmRef struct {
	id, arn string
	queues  []childParent
	fleets  []childParent
}

func deadlineFarmARN(region, acct, farmID string) string {
	return fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", region, acct, farmID)
}

// scanDeadline runs the member phases after every older phase, over the farms,
// fleets and queues the older phases stored before any failure, so a member
// phase's failure costs the older phases no rows, and an older phase's failure
// costs the member phases only the members of parents it never stored. The
// older side's demotable transient error is demoted to a warning, since one
// transient member would make the dispatcher read the whole join as transient,
// reporting a member phase's hard error as a transient one.
// The member phases skip a parent still transient after the SDK's retries
// (childFanOutSkipTransient), so no transient error leaves them. The errors are
// joined and the counts summed.
func scanDeadline(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := deadline.NewFromConfig(acct.cfg, func(o *deadline.Options) { o.Region = region })

	farms, total, inserted, err := scanDeadlineOlderPhases(ctx, client, acct, region, st, scanID)
	errs := []error{demoteTransient(st, "deadline", acct, region, err)}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDeadlineFarmMembers(ctx, client, acct, region, st, scanID, farms) },
		func() (int, int, error) {
			return scanDeadlineFleetMembers(ctx, client, acct, region, st, scanID, farms)
		},
		func() (int, int, error) {
			return scanDeadlineQueueMembers(ctx, client, acct, region, st, scanID, farms)
		},
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

// scanDeadlineOlderPhases runs the phases that predate the member phases,
// stopping at the first error any of them returns, transient ones included.
// It returns the stored farms, each with the queues and fleets stored before
// it stopped.
func scanDeadlineOlderPhases(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string) (farms []dlFarmRef, total, inserted int, err error) {
	// Phase 1: farms (top-level). Collect IDs for per-farm fan-out.
	farms, t, i, ferr := scanDeadlineFarms(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return nil, total, inserted, ferr
	}
	total += t
	inserted += i

	// Per-farm phases.
	for fi := range farms {
		fr := &farms[fi]
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanDeadlineFleets(ctx, client, acct, region, st, scanID, fr) },
			func() (int, int, error) { return scanDeadlineLimits(ctx, client, acct, region, st, scanID, fr) },
			func() (int, int, error) {
				return scanDeadlineStorageProfiles(ctx, client, acct, region, st, scanID, fr)
			},
			func() (int, int, error) { return scanDeadlineBudgets(ctx, client, acct, region, st, scanID, fr) },
			func() (int, int, error) { return scanDeadlineQueues(ctx, client, acct, region, st, scanID, fr) },
			func() (int, int, error) {
				return scanDeadlineQueueFleetAssociations(ctx, client, acct, region, st, scanID, fr)
			},
			func() (int, int, error) {
				return scanDeadlineQueueLimitAssociations(ctx, client, acct, region, st, scanID, fr)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return farms, total, inserted, perr
			}
			total += t
			inserted += i
		}
		// Per-(farm, queue) phase.
		for _, qr := range fr.queues {
			t, i, perr := scanDeadlineQueueEnvironments(ctx, client, acct, region, st, scanID, fr, qr)
			if perr != nil {
				return farms, total, inserted, perr
			}
			total += t
			inserted += i
		}
		// Per-(farm, fleet) phase: ListVolumes requires both FarmId and FleetId.
		for _, fleet := range fr.fleets {
			t, i, perr := scanDeadlineVolumes(ctx, client, acct, region, st, scanID, fr, fleet.id)
			if perr != nil {
				return farms, total, inserted, perr
			}
			total += t
			inserted += i
		}
	}

	// Phase: license-endpoints (top-level). Collect IDs for per-LE metered-products.
	leIDs, t, i, ferr := scanDeadlineLicenseEndpoints(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return farms, total, inserted, ferr
	}
	total += t
	inserted += i

	// Per-license-endpoint metered-products.
	for _, leID := range leIDs {
		t, i, perr := scanDeadlineMeteredProducts(ctx, client, acct, region, st, scanID, leID)
		if perr != nil {
			return farms, total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Phase: monitors (top-level).
	t, i, ferr = scanDeadlineMonitors(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return farms, total, inserted, ferr
	}
	total += t
	inserted += i
	return farms, total, inserted, nil
}

func scanDeadlineFarms(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string) ([]dlFarmRef, int, int, error) {
	pager := deadline.NewListFarmsPaginator(client, &deadline.ListFarmsInput{})
	var batch []*store.Resource
	var refs []dlFarmRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListFarms", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("deadline:ListFarms: %w", perr)
		}
		for _, f := range out.Farms {
			id := sv(f.FarmId)
			if id == "" {
				continue
			}
			arn := deadlineFarmARN(region, acct.ID, id)
			label := sv(f.DisplayName)
			if label == "" {
				label = id
			}
			refs = append(refs, dlFarmRef{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineFarm, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "deadline farms")
	return refs, t, i, err
}

func scanDeadlineFleets(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListFleetsPaginator(client, &deadline.ListFleetsInput{FarmId: &fr.id})
	var batch []*store.Resource
	var fleets []childParent
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListFleets: %w", perr)
		}
		for _, f := range out.Fleets {
			id := sv(f.FleetId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/fleet/%s", fr.arn, id)
			fleets = append(fleets, childParent{id: id, arn: arn})
			label := sv(f.DisplayName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "deadline fleets")
	if err == nil {
		fr.fleets = fleets
	}
	return t, i, err
}

func scanDeadlineLimits(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListLimitsPaginator(client, &deadline.ListLimitsInput{FarmId: &fr.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListLimits", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListLimits: %w", perr)
		}
		for _, l := range out.Limits {
			id := sv(l.LimitId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/limit/%s", fr.arn, id)
			label := sv(l.DisplayName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineLimit, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline limits")
}

func scanDeadlineMeteredProducts(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID, licenseEndpointID string) (int, int, error) {
	pager := deadline.NewListMeteredProductsPaginator(client, &deadline.ListMeteredProductsInput{LicenseEndpointId: &licenseEndpointID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListMeteredProducts", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListMeteredProducts: %w", perr)
		}
		for _, m := range out.MeteredProducts {
			pid := sv(m.ProductId)
			if pid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:deadline:%s:%s:license-endpoint/%s/metered-product/%s", region, acct.ID, licenseEndpointID, pid)
			label := pid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineMeteredProduct, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline metered-products")
}

func scanDeadlineStorageProfiles(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListStorageProfilesPaginator(client, &deadline.ListStorageProfilesInput{FarmId: &fr.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListStorageProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListStorageProfiles: %w", perr)
		}
		for _, sp := range out.StorageProfiles {
			id := sv(sp.StorageProfileId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/storage-profile/%s", fr.arn, id)
			label := sv(sp.DisplayName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineStorageProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sp), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline storage-profiles")
}

func scanDeadlineQueues(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListQueuesPaginator(client, &deadline.ListQueuesInput{FarmId: &fr.id})
	var batch []*store.Resource
	var queues []childParent
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListQueues", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListQueues: %w", perr)
		}
		for _, q := range out.Queues {
			id := sv(q.QueueId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/queue/%s", fr.arn, id)
			label := sv(q.DisplayName)
			if label == "" {
				label = id
			}
			queues = append(queues, childParent{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineQueue, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "deadline queues")
	if err == nil {
		fr.queues = queues
	}
	return t, i, err
}

func scanDeadlineQueueFleetAssociations(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListQueueFleetAssociationsPaginator(client, &deadline.ListQueueFleetAssociationsInput{FarmId: &fr.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListQueueFleetAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListQueueFleetAssociations: %w", perr)
		}
		for _, a := range out.QueueFleetAssociations {
			qid := sv(a.QueueId)
			fid := sv(a.FleetId)
			if qid == "" || fid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/queue/%s/fleet/%s/association", fr.arn, qid, fid)
			label := fmt.Sprintf("%s/%s", qid, fid)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineQueueFleetAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline queue-fleet-associations")
}

func scanDeadlineQueueLimitAssociations(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListQueueLimitAssociationsPaginator(client, &deadline.ListQueueLimitAssociationsInput{FarmId: &fr.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListQueueLimitAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListQueueLimitAssociations: %w", perr)
		}
		for _, a := range out.QueueLimitAssociations {
			qid := sv(a.QueueId)
			lid := sv(a.LimitId)
			if qid == "" || lid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/queue/%s/limit/%s/association", fr.arn, qid, lid)
			label := fmt.Sprintf("%s/%s", qid, lid)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineQueueLimitAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline queue-limit-associations")
}

func scanDeadlineQueueEnvironments(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef, qr childParent) (int, int, error) {
	pager := deadline.NewListQueueEnvironmentsPaginator(client, &deadline.ListQueueEnvironmentsInput{FarmId: &fr.id, QueueId: &qr.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListQueueEnvironments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListQueueEnvironments: %w", perr)
		}
		for _, e := range out.Environments {
			id := sv(e.QueueEnvironmentId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/queue-environment/%s", qr.arn, id)
			label := sv(e.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineQueueEnvironment, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline queue-environments")
}

func scanDeadlineLicenseEndpoints(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := deadline.NewListLicenseEndpointsPaginator(client, &deadline.ListLicenseEndpointsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListLicenseEndpoints", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("deadline:ListLicenseEndpoints: %w", perr)
		}
		for _, le := range out.LicenseEndpoints {
			id := sv(le.LicenseEndpointId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:deadline:%s:%s:license-endpoint/%s", region, acct.ID, id)
			label := id
			ids = append(ids, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineLicenseEndpoint, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(le), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "deadline license-endpoints")
	return ids, t, i, err
}

func scanDeadlineMonitors(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := deadline.NewListMonitorsPaginator(client, &deadline.ListMonitorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListMonitors", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListMonitors: %w", perr)
		}
		for _, m := range out.Monitors {
			id := sv(m.MonitorId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:deadline:%s:%s:monitor/%s", region, acct.ID, id)
			label := sv(m.DisplayName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineMonitor, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline monitors")
}
