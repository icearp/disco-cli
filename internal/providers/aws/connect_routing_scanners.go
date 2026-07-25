package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectQueue, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectRoutingProfile, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectHoursOfOperation, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectAgentStatus, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectQuickConnect, Service: "connect"})
}

// connectRoutingAPI is the narrow surface used by the Routing family.
// All five resources are per-Instance: List takes InstanceId, Describe
// takes (InstanceId, ResourceId).
type connectRoutingAPI interface {
	ListQueues(context.Context, *connect.ListQueuesInput, ...func(*connect.Options)) (*connect.ListQueuesOutput, error)
	DescribeQueue(context.Context, *connect.DescribeQueueInput, ...func(*connect.Options)) (*connect.DescribeQueueOutput, error)
	ListRoutingProfiles(context.Context, *connect.ListRoutingProfilesInput, ...func(*connect.Options)) (*connect.ListRoutingProfilesOutput, error)
	DescribeRoutingProfile(context.Context, *connect.DescribeRoutingProfileInput, ...func(*connect.Options)) (*connect.DescribeRoutingProfileOutput, error)
	ListHoursOfOperations(context.Context, *connect.ListHoursOfOperationsInput, ...func(*connect.Options)) (*connect.ListHoursOfOperationsOutput, error)
	DescribeHoursOfOperation(context.Context, *connect.DescribeHoursOfOperationInput, ...func(*connect.Options)) (*connect.DescribeHoursOfOperationOutput, error)
	ListAgentStatuses(context.Context, *connect.ListAgentStatusesInput, ...func(*connect.Options)) (*connect.ListAgentStatusesOutput, error)
	DescribeAgentStatus(context.Context, *connect.DescribeAgentStatusInput, ...func(*connect.Options)) (*connect.DescribeAgentStatusOutput, error)
	ListQuickConnects(context.Context, *connect.ListQuickConnectsInput, ...func(*connect.Options)) (*connect.ListQuickConnectsOutput, error)
	DescribeQuickConnect(context.Context, *connect.DescribeQuickConnectInput, ...func(*connect.Options)) (*connect.DescribeQuickConnectOutput, error)
}

// scanConnectRouting fans out the five Routing-family phases per instance.
func scanConnectRouting(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanConnectQueues(ctx, client, instances, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanConnectRoutingProfiles(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectHoursOfOperations(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectAgentStatuses(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectQuickConnects(ctx, client, instances, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// connectInstanceItem carries (instanceID, resourceID) for per-instance
// Describe fan-out.
type connectInstanceItem struct {
	instanceID, id string
}

// connectPerInstanceFanout runs build per (instanceID, id) concurrently and
// upserts the batch. Mirrors connectDescribeFanout but with two-tuple keys.
func connectPerInstanceFanout(
	ctx context.Context,
	items []connectInstanceItem,
	weight int64,
	build func(context.Context, connectInstanceItem) (*store.Resource, error),
	st *store.Store,
	label string,
) (int, int, error) {
	if len(items) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(weight)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range items {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			r, err := build(gctx, k)
			if err != nil {
				return err
			}
			if r == nil {
				return nil
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, uerr)
	}
	return len(batch), n, nil
}

func scanConnectQueues(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListQueuesPaginator(client, &connect.ListQueuesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListQueues", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListQueues %s: %w", instID, perr)
			}
			for _, q := range out.QueueSummaryList {
				if q.Id != nil {
					items = append(items, connectInstanceItem{instID, *q.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeQueue(gctx, &connect.DescribeQueueInput{InstanceId: &k.instanceID, QueueId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeQueue %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.Queue == nil {
			return nil, nil
		}
		arn := sv(out.Queue.QueueArn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.Queue.Name)
		status := string(out.Queue.Status)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectQueue,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect queues")
}

func scanConnectRoutingProfiles(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListRoutingProfilesPaginator(client, &connect.ListRoutingProfilesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListRoutingProfiles", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListRoutingProfiles %s: %w", instID, perr)
			}
			for _, r := range out.RoutingProfileSummaryList {
				if r.Id != nil {
					items = append(items, connectInstanceItem{instID, *r.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeRoutingProfile(gctx, &connect.DescribeRoutingProfileInput{InstanceId: &k.instanceID, RoutingProfileId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeRoutingProfile %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.RoutingProfile == nil {
			return nil, nil
		}
		arn := sv(out.RoutingProfile.RoutingProfileArn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.RoutingProfile.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectRoutingProfile,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect routing profiles")
}

func scanConnectHoursOfOperations(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListHoursOfOperationsPaginator(client, &connect.ListHoursOfOperationsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListHoursOfOperations", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListHoursOfOperations %s: %w", instID, perr)
			}
			for _, h := range out.HoursOfOperationSummaryList {
				if h.Id != nil {
					items = append(items, connectInstanceItem{instID, *h.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeHoursOfOperation(gctx, &connect.DescribeHoursOfOperationInput{InstanceId: &k.instanceID, HoursOfOperationId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeHoursOfOperation %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.HoursOfOperation == nil {
			return nil, nil
		}
		arn := sv(out.HoursOfOperation.HoursOfOperationArn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.HoursOfOperation.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectHoursOfOperation,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect hours of operations")
}

func scanConnectAgentStatuses(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListAgentStatusesPaginator(client, &connect.ListAgentStatusesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListAgentStatuses", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListAgentStatuses %s: %w", instID, perr)
			}
			for _, a := range out.AgentStatusSummaryList {
				if a.Id != nil {
					items = append(items, connectInstanceItem{instID, *a.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeAgentStatus(gctx, &connect.DescribeAgentStatusInput{InstanceId: &k.instanceID, AgentStatusId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeAgentStatus %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.AgentStatus == nil {
			return nil, nil
		}
		arn := sv(out.AgentStatus.AgentStatusARN)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.AgentStatus.Name)
		state := string(out.AgentStatus.State)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectAgentStatus,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &state,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect agent statuses")
}

func scanConnectQuickConnects(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListQuickConnectsPaginator(client, &connect.ListQuickConnectsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListQuickConnects", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListQuickConnects %s: %w", instID, perr)
			}
			for _, q := range out.QuickConnectSummaryList {
				if q.Id != nil {
					items = append(items, connectInstanceItem{instID, *q.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeQuickConnect(gctx, &connect.DescribeQuickConnectInput{InstanceId: &k.instanceID, QuickConnectId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeQuickConnect %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.QuickConnect == nil {
			return nil, nil
		}
		arn := sv(out.QuickConnect.QuickConnectARN)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.QuickConnect.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectQuickConnect,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect quick connects")
}
