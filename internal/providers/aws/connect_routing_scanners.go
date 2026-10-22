package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

// scanConnectRouting runs the five Routing-family phases per instance.
func scanConnectRouting(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	return connectRunPhases(st, acct, region,
		connectPhase{"connect queues", func() (int, int, error) {
			return scanConnectQueues(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect routing profiles", func() (int, int, error) {
			return scanConnectRoutingProfiles(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect hours of operations", func() (int, int, error) {
			return scanConnectHoursOfOperations(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect agent statuses", func() (int, int, error) {
			return scanConnectAgentStatuses(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect quick connects", func() (int, int, error) {
			return scanConnectQuickConnects(ctx, client, instances, acct, region, st, scanID)
		}},
	)
}

func scanConnectQueues(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListQueues", instances,
		func(instID string) connectPager[*connect.ListQueuesOutput] {
			return connect.NewListQueuesPaginator(client, &connect.ListQueuesInput{InstanceId: &instID},
				func(o *connect.ListQueuesPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListQueuesOutput) []cttypes.QueueSummary { return o.QueueSummaryList },
		func(s cttypes.QueueSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.QueueSummary]{
		rtype: TypeConnectQueue, op: "connect:DescribeQueue", label: "connect queues",
		describe: func(ctx context.Context, it connectListed[cttypes.QueueSummary], r *store.Resource) error {
			out, err := client.DescribeQueue(ctx, &connect.DescribeQueueInput{InstanceId: &it.instanceID, QueueId: &it.id})
			if err != nil {
				return err
			}
			q := out.Queue
			if q == nil {
				return errConnectNoBody
			}
			name, status := sv(q.Name), string(q.Status)
			r.Name, r.Status = &name, &status
			r.TagsJSON = mapTagsJSON(q.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.QueueSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectRoutingProfiles(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListRoutingProfiles", instances,
		func(instID string) connectPager[*connect.ListRoutingProfilesOutput] {
			return connect.NewListRoutingProfilesPaginator(client, &connect.ListRoutingProfilesInput{InstanceId: &instID},
				func(o *connect.ListRoutingProfilesPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListRoutingProfilesOutput) []cttypes.RoutingProfileSummary {
			return o.RoutingProfileSummaryList
		},
		func(s cttypes.RoutingProfileSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.RoutingProfileSummary]{
		rtype: TypeConnectRoutingProfile, op: "connect:DescribeRoutingProfile", label: "connect routing profiles",
		describe: func(ctx context.Context, it connectListed[cttypes.RoutingProfileSummary], r *store.Resource) error {
			out, err := client.DescribeRoutingProfile(ctx, &connect.DescribeRoutingProfileInput{InstanceId: &it.instanceID, RoutingProfileId: &it.id})
			if err != nil {
				return err
			}
			p := out.RoutingProfile
			if p == nil {
				return errConnectNoBody
			}
			name := sv(p.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(p.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.RoutingProfileSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectHoursOfOperations(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListHoursOfOperations", instances,
		func(instID string) connectPager[*connect.ListHoursOfOperationsOutput] {
			return connect.NewListHoursOfOperationsPaginator(client, &connect.ListHoursOfOperationsInput{InstanceId: &instID},
				func(o *connect.ListHoursOfOperationsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListHoursOfOperationsOutput) []cttypes.HoursOfOperationSummary {
			return o.HoursOfOperationSummaryList
		},
		func(s cttypes.HoursOfOperationSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.HoursOfOperationSummary]{
		rtype: TypeConnectHoursOfOperation, op: "connect:DescribeHoursOfOperation", label: "connect hours of operations",
		describe: func(ctx context.Context, it connectListed[cttypes.HoursOfOperationSummary], r *store.Resource) error {
			out, err := client.DescribeHoursOfOperation(ctx, &connect.DescribeHoursOfOperationInput{InstanceId: &it.instanceID, HoursOfOperationId: &it.id})
			if err != nil {
				return err
			}
			h := out.HoursOfOperation
			if h == nil {
				return errConnectNoBody
			}
			name := sv(h.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(h.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.HoursOfOperationSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectAgentStatuses(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListAgentStatuses", instances,
		func(instID string) connectPager[*connect.ListAgentStatusesOutput] {
			return connect.NewListAgentStatusesPaginator(client, &connect.ListAgentStatusesInput{InstanceId: &instID},
				func(o *connect.ListAgentStatusesPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListAgentStatusesOutput) []cttypes.AgentStatusSummary { return o.AgentStatusSummaryList },
		func(s cttypes.AgentStatusSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.AgentStatusSummary]{
		rtype: TypeConnectAgentStatus, op: "connect:DescribeAgentStatus", label: "connect agent statuses",
		describe: func(ctx context.Context, it connectListed[cttypes.AgentStatusSummary], r *store.Resource) error {
			out, err := client.DescribeAgentStatus(ctx, &connect.DescribeAgentStatusInput{InstanceId: &it.instanceID, AgentStatusId: &it.id})
			if err != nil {
				return err
			}
			a := out.AgentStatus
			if a == nil {
				return errConnectNoBody
			}
			name, state := sv(a.Name), string(a.State)
			r.Name, r.Status = &name, &state
			r.TagsJSON = mapTagsJSON(a.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.AgentStatusSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectQuickConnects(ctx context.Context, client connectRoutingAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListQuickConnects", instances,
		func(instID string) connectPager[*connect.ListQuickConnectsOutput] {
			return connect.NewListQuickConnectsPaginator(client, &connect.ListQuickConnectsInput{InstanceId: &instID},
				func(o *connect.ListQuickConnectsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListQuickConnectsOutput) []cttypes.QuickConnectSummary {
			return o.QuickConnectSummaryList
		},
		func(s cttypes.QuickConnectSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.QuickConnectSummary]{
		rtype: TypeConnectQuickConnect, op: "connect:DescribeQuickConnect", label: "connect quick connects",
		describe: func(ctx context.Context, it connectListed[cttypes.QuickConnectSummary], r *store.Resource) error {
			out, err := client.DescribeQuickConnect(ctx, &connect.DescribeQuickConnectInput{InstanceId: &it.instanceID, QuickConnectId: &it.id})
			if err != nil {
				return err
			}
			q := out.QuickConnect
			if q == nil {
				return errConnectNoBody
			}
			name := sv(q.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(q.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.QuickConnectSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}
