package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/medialive"
)

func init() {
	registerService(serviceEntry{
		name: "aws:medialive",
		fn:   scanMediaLive,
		emits: []coverage.TypeDecl{
			{Service: "medialive", DiscoType: TypeMediaLiveChannel},
			{Service: "medialive", DiscoType: TypeMediaLiveChannelPlacementGroup},
			{Service: "medialive", DiscoType: TypeMediaLiveCloudWatchAlarmTemplate},
			{Service: "medialive", DiscoType: TypeMediaLiveCloudWatchAlarmTemplateGroup, Leaf: true},
			{Service: "medialive", DiscoType: TypeMediaLiveCluster},
			{Service: "medialive", DiscoType: TypeMediaLiveEventBridgeRuleTemplate},
			{Service: "medialive", DiscoType: TypeMediaLiveEventBridgeRuleTemplateGroup, Leaf: true},
			{Service: "medialive", DiscoType: TypeMediaLiveInput},
			{Service: "medialive", DiscoType: TypeMediaLiveInputDevice, Leaf: true},
			{Service: "medialive", DiscoType: TypeMediaLiveInputSecurityGroup},
			{Service: "medialive", DiscoType: TypeMediaLiveNode, Leaf: true},
			{Service: "medialive", DiscoType: TypeMediaLiveReservation, Leaf: true},
			{Service: "medialive", DiscoType: TypeMediaLiveMultiplex, Leaf: true},
			{Service: "medialive", DiscoType: TypeMediaLiveMultiplexProgram},
			{Service: "medialive", DiscoType: TypeMediaLiveNetwork},
			{Service: "medialive", DiscoType: TypeMediaLiveSdiSource},
			{Service: "medialive", DiscoType: TypeMediaLiveSignalMap, Leaf: true},
		},
	})
}

type mediaLiveAPI interface {
	ListChannels(context.Context, *medialive.ListChannelsInput, ...func(*medialive.Options)) (*medialive.ListChannelsOutput, error)
	ListClusters(context.Context, *medialive.ListClustersInput, ...func(*medialive.Options)) (*medialive.ListClustersOutput, error)
	ListChannelPlacementGroups(context.Context, *medialive.ListChannelPlacementGroupsInput, ...func(*medialive.Options)) (*medialive.ListChannelPlacementGroupsOutput, error)
	ListCloudWatchAlarmTemplates(context.Context, *medialive.ListCloudWatchAlarmTemplatesInput, ...func(*medialive.Options)) (*medialive.ListCloudWatchAlarmTemplatesOutput, error)
	ListCloudWatchAlarmTemplateGroups(context.Context, *medialive.ListCloudWatchAlarmTemplateGroupsInput, ...func(*medialive.Options)) (*medialive.ListCloudWatchAlarmTemplateGroupsOutput, error)
	ListEventBridgeRuleTemplates(context.Context, *medialive.ListEventBridgeRuleTemplatesInput, ...func(*medialive.Options)) (*medialive.ListEventBridgeRuleTemplatesOutput, error)
	ListEventBridgeRuleTemplateGroups(context.Context, *medialive.ListEventBridgeRuleTemplateGroupsInput, ...func(*medialive.Options)) (*medialive.ListEventBridgeRuleTemplateGroupsOutput, error)
	ListInputs(context.Context, *medialive.ListInputsInput, ...func(*medialive.Options)) (*medialive.ListInputsOutput, error)
	ListInputDevices(context.Context, *medialive.ListInputDevicesInput, ...func(*medialive.Options)) (*medialive.ListInputDevicesOutput, error)
	ListInputSecurityGroups(context.Context, *medialive.ListInputSecurityGroupsInput, ...func(*medialive.Options)) (*medialive.ListInputSecurityGroupsOutput, error)
	ListNodes(context.Context, *medialive.ListNodesInput, ...func(*medialive.Options)) (*medialive.ListNodesOutput, error)
	ListReservations(context.Context, *medialive.ListReservationsInput, ...func(*medialive.Options)) (*medialive.ListReservationsOutput, error)
	ListMultiplexes(context.Context, *medialive.ListMultiplexesInput, ...func(*medialive.Options)) (*medialive.ListMultiplexesOutput, error)
	ListMultiplexPrograms(context.Context, *medialive.ListMultiplexProgramsInput, ...func(*medialive.Options)) (*medialive.ListMultiplexProgramsOutput, error)
	ListNetworks(context.Context, *medialive.ListNetworksInput, ...func(*medialive.Options)) (*medialive.ListNetworksOutput, error)
	ListSdiSources(context.Context, *medialive.ListSdiSourcesInput, ...func(*medialive.Options)) (*medialive.ListSdiSourcesOutput, error)
	ListSignalMaps(context.Context, *medialive.ListSignalMapsInput, ...func(*medialive.Options)) (*medialive.ListSignalMapsOutput, error)
}

func scanMediaLive(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := medialive.NewFromConfig(acct.cfg, func(o *medialive.Options) { o.Region = region })

	clusterIDs, t, i, ferr := scanMLClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	multiplexIDs, t, i, ferr := scanMLMultiplexes(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanMLChannels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanMLChannelPlacementGroups(ctx, client, acct, region, st, scanID, clusterIDs)
		},
		func() (int, int, error) { return scanMLAlarmTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLAlarmTemplateGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLEBTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLEBTemplateGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLInputs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLInputDevices(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLInputSecurityGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLNodes(ctx, client, acct, region, st, scanID, clusterIDs) },
		func() (int, int, error) { return scanMLReservations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanMLMultiplexPrograms(ctx, client, acct, region, st, scanID, multiplexIDs)
		},
		func() (int, int, error) { return scanMLNetworks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLSdiSources(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMLSignalMaps(ctx, client, acct, region, st, scanID) },
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

func mlARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:medialive:%s:%s:%s/%s", region, acct, kind, id)
}

func scanMLChannels(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListChannelsPaginator(client, &medialive.ListChannelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListChannels", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListChannels: %w", perr)
		}
		for _, c := range out.Channels {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveChannel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive channels")
}

func scanMLClusters(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := medialive.NewListClustersPaginator(client, &medialive.ListClustersInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListClusters", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			// MediaLive Anywhere (clusters) isn't offered in every region; those
			// reject ListClusters with 404 NotFoundException. Per-region
			// availability gap — silent-skip.
			if isAPIErrorCode(perr, "NotFoundException") {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("medialive:ListClusters: %w", perr)
		}
		for _, c := range out.Clusters {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			id := sv(c.Id)
			ids = append(ids, id)
			label := sv(c.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveCluster, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "medialive clusters")
	return ids, t, i, err
}

func scanMLChannelPlacementGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string, clusterIDs []string) (int, int, error) {
	if len(clusterIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, cid := range clusterIDs {
		id := cid
		pager := medialive.NewListChannelPlacementGroupsPaginator(client, &medialive.ListChannelPlacementGroupsInput{ClusterId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("medialive:ListChannelPlacementGroups %s: %w", cid, perr)
			}
			for _, p := range out.ChannelPlacementGroups {
				arn := sv(p.Arn)
				if arn == "" {
					continue
				}
				label := sv(p.Name)
				if label == "" {
					label = sv(p.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeMediaLiveChannelPlacementGroup, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "medialive channel-placement-groups")
}

func scanMLAlarmTemplates(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListCloudWatchAlarmTemplatesPaginator(client, &medialive.ListCloudWatchAlarmTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListCloudWatchAlarmTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListCloudWatchAlarmTemplates: %w", perr)
		}
		for _, a := range out.CloudWatchAlarmTemplates {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveCloudWatchAlarmTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				// AWS-supplied default templates carry an Id prefixed "aws-".
				ManagedByProvider: strings.HasPrefix(sv(a.Id), "aws-"),
			})
		}
	}
	return upsertBatch(st, batch, "medialive cloudwatch-alarm-templates")
}

func scanMLAlarmTemplateGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListCloudWatchAlarmTemplateGroupsPaginator(client, &medialive.ListCloudWatchAlarmTemplateGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListCloudWatchAlarmTemplateGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListCloudWatchAlarmTemplateGroups: %w", perr)
		}
		for _, g := range out.CloudWatchAlarmTemplateGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveCloudWatchAlarmTemplateGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				// AWS-supplied default groups carry an Id prefixed "aws-";
				// customer groups get a generated alphanumeric Id.
				ManagedByProvider: strings.HasPrefix(sv(g.Id), "aws-"),
			})
		}
	}
	return upsertBatch(st, batch, "medialive cloudwatch-alarm-template-groups")
}

func scanMLEBTemplates(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListEventBridgeRuleTemplatesPaginator(client, &medialive.ListEventBridgeRuleTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListEventBridgeRuleTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListEventBridgeRuleTemplates: %w", perr)
		}
		for _, e := range out.EventBridgeRuleTemplates {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			label := sv(e.Name)
			if label == "" {
				label = sv(e.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveEventBridgeRuleTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive eventbridge-rule-templates")
}

func scanMLEBTemplateGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListEventBridgeRuleTemplateGroupsPaginator(client, &medialive.ListEventBridgeRuleTemplateGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListEventBridgeRuleTemplateGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListEventBridgeRuleTemplateGroups: %w", perr)
		}
		for _, g := range out.EventBridgeRuleTemplateGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveEventBridgeRuleTemplateGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive eventbridge-rule-template-groups")
}

func scanMLInputs(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListInputsPaginator(client, &medialive.ListInputsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListInputs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListInputs: %w", perr)
		}
		for _, i := range out.Inputs {
			arn := sv(i.Arn)
			if arn == "" {
				continue
			}
			label := sv(i.Name)
			if label == "" {
				label = sv(i.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveInput, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(i), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive inputs")
}

func scanMLInputDevices(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListInputDevicesPaginator(client, &medialive.ListInputDevicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListInputDevices", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListInputDevices: %w", perr)
		}
		for _, d := range out.InputDevices {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveInputDevice, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive input-devices")
}

// scanMLNodes — ListNodes requires a ClusterId, so fan out over the clusters
// discovered by scanMLClusters. DescribeNodeSummary carries a real distinct
// node ARN, used directly as the NativeID (Leaf).
func scanMLNodes(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string, clusterIDs []string) (int, int, error) {
	if len(clusterIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, cid := range clusterIDs {
		id := cid
		pager := medialive.NewListNodesPaginator(client, &medialive.ListNodesInput{ClusterId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("medialive:ListNodes %s: %w", cid, perr)
			}
			for _, n := range out.Nodes {
				arn := sv(n.Arn)
				if arn == "" {
					continue
				}
				label := sv(n.Name)
				if label == "" {
					label = sv(n.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeMediaLiveNode, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "medialive nodes")
}

func scanMLReservations(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListReservationsPaginator(client, &medialive.ListReservationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListReservations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListReservations: %w", perr)
		}
		for _, r := range out.Reservations {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = arn
			}
			status := string(r.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveReservation, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive reservations")
}

func scanMLInputSecurityGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListInputSecurityGroupsPaginator(client, &medialive.ListInputSecurityGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListInputSecurityGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListInputSecurityGroups: %w", perr)
		}
		for _, g := range out.InputSecurityGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveInputSecurityGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive input-security-groups")
}

func scanMLMultiplexes(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := medialive.NewListMultiplexesPaginator(client, &medialive.ListMultiplexesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListMultiplexes", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("medialive:ListMultiplexes: %w", perr)
		}
		for _, m := range out.Multiplexes {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			id := sv(m.Id)
			ids = append(ids, id)
			label := sv(m.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveMultiplex, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "medialive multiplexes")
	return ids, t, i, err
}

// scanMLMultiplexPrograms — MultiplexProgramSummary has no ARN; synth from
// (multiplexId, programName).
func scanMLMultiplexPrograms(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string, multiplexIDs []string) (int, int, error) {
	if len(multiplexIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, mid := range multiplexIDs {
		id := mid
		pager := medialive.NewListMultiplexProgramsPaginator(client, &medialive.ListMultiplexProgramsInput{MultiplexId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("medialive:ListMultiplexPrograms %s: %w", mid, perr)
			}
			for _, p := range out.MultiplexPrograms {
				name := sv(p.ProgramName)
				if name == "" {
					continue
				}
				arn := mlARN(region, acct.ID, "multiplexprogram", mid+"/"+name)
				label := name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeMediaLiveMultiplexProgram, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "medialive multiplex-programs")
}

func scanMLNetworks(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListNetworksPaginator(client, &medialive.ListNetworksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListNetworks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListNetworks: %w", perr)
		}
		for _, n := range out.Networks {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			label := sv(n.Name)
			if label == "" {
				label = sv(n.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveNetwork, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive networks")
}

func scanMLSdiSources(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListSdiSourcesPaginator(client, &medialive.ListSdiSourcesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListSdiSources", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListSdiSources: %w", perr)
		}
		for _, s := range out.SdiSources {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveSdiSource, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive sdi-sources")
}

func scanMLSignalMaps(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := medialive.NewListSignalMapsPaginator(client, &medialive.ListSignalMapsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "medialive:ListSignalMaps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("medialive:ListSignalMaps: %w", perr)
		}
		for _, s := range out.SignalMaps {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaLiveSignalMap, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "medialive signal-maps")
}
