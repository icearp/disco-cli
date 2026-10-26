package aws

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/medialive"
	medialivetypes "github.com/aws/aws-sdk-go-v2/service/medialive/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaLiveChannel, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveChannelPlacementGroup, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveCloudWatchAlarmTemplate, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveCloudWatchAlarmTemplateGroup, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveCluster, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveEventBridgeRuleTemplate, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveEventBridgeRuleTemplateGroup, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveInput, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveInputDevice, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveInputSecurityGroup, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveNode, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveReservation, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveMultiplex, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveMultiplexProgram, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveNetwork, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveSdiSource, Service: "medialive"})
	registerType(restype.Descriptor{Type: TypeMediaLiveSignalMap, Service: "medialive"})
	registerService(serviceEntry{
		name: "aws:medialive",
		fn:   scanMediaLive,
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

const (
	mediaLiveListChannelsOp                = "medialive:ListChannels"
	mediaLiveListClustersOp                = "medialive:ListClusters"
	mediaLiveListPlacementGroupsOp         = "medialive:ListChannelPlacementGroups"
	mediaLiveListAlarmTemplatesOp          = "medialive:ListCloudWatchAlarmTemplates"
	mediaLiveListAlarmTemplateGroupsOp     = "medialive:ListCloudWatchAlarmTemplateGroups"
	mediaLiveListEBRuleTemplatesOp         = "medialive:ListEventBridgeRuleTemplates"
	mediaLiveListEBRuleTemplateGroupsOp    = "medialive:ListEventBridgeRuleTemplateGroups"
	mediaLiveListInputsOp                  = "medialive:ListInputs"
	mediaLiveListInputDevicesOp            = "medialive:ListInputDevices"
	mediaLiveListInputSecurityGroupsOp     = "medialive:ListInputSecurityGroups"
	mediaLiveListNodesOp                   = "medialive:ListNodes"
	mediaLiveListReservationsOp            = "medialive:ListReservations"
	mediaLiveListMultiplexesOp             = "medialive:ListMultiplexes"
	mediaLiveListMultiplexProgramsOp       = "medialive:ListMultiplexPrograms"
	mediaLiveListNetworksOp                = "medialive:ListNetworks"
	mediaLiveListSdiSourcesOp              = "medialive:ListSdiSources"
	mediaLiveListSignalMapsOp              = "medialive:ListSignalMaps"
	mediaLiveMultiplexProgramNotFoundError = "NotFoundException"
)

func scanMediaLive(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := medialive.NewFromConfig(acct.cfg, func(o *medialive.Options) { o.Region = region })
	return scanMLAll(ctx, client, acct, region, st, scanID)
}

// scanMLAll runs every phase and joins their errors; only a store write
// failure stops it. A failed cluster or multiplex listing leaves its child
// phases fewer parents to list.
//
// Every list call leaves MaxResults unset, so the service picks the page
// size: MediaLive's model gives MaxResults no range, so there is no
// documented cap to ask for.
func scanMLAll(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	clusterIDs, t, i, ferr := scanMLClusters(ctx, client, acct, region, st, scanID)
	if phases.addPhase(mediaLiveListClustersOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	multiplexIDs, t, i, ferr := scanMLMultiplexes(ctx, client, acct, region, st, scanID)
	if phases.addPhase(mediaLiveListMultiplexesOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{mediaLiveListChannelsOp, func() (int, int, error) { return scanMLChannels(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListPlacementGroupsOp, func() (int, int, error) {
			return scanMLChannelPlacementGroups(ctx, client, acct, region, st, scanID, clusterIDs)
		}},
		{mediaLiveListAlarmTemplatesOp, func() (int, int, error) { return scanMLAlarmTemplates(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListAlarmTemplateGroupsOp, func() (int, int, error) {
			return scanMLAlarmTemplateGroups(ctx, client, acct, region, st, scanID)
		}},
		{mediaLiveListEBRuleTemplatesOp, func() (int, int, error) { return scanMLEBTemplates(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListEBRuleTemplateGroupsOp, func() (int, int, error) {
			return scanMLEBTemplateGroups(ctx, client, acct, region, st, scanID)
		}},
		{mediaLiveListInputsOp, func() (int, int, error) { return scanMLInputs(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListInputDevicesOp, func() (int, int, error) { return scanMLInputDevices(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListInputSecurityGroupsOp, func() (int, int, error) {
			return scanMLInputSecurityGroups(ctx, client, acct, region, st, scanID)
		}},
		{mediaLiveListNodesOp, func() (int, int, error) { return scanMLNodes(ctx, client, acct, region, st, scanID, clusterIDs) }},
		{mediaLiveListReservationsOp, func() (int, int, error) { return scanMLReservations(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListMultiplexProgramsOp, func() (int, int, error) {
			return scanMLMultiplexPrograms(ctx, client, acct, region, st, scanID, multiplexIDs)
		}},
		{mediaLiveListNetworksOp, func() (int, int, error) { return scanMLNetworks(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListSdiSourcesOp, func() (int, int, error) { return scanMLSdiSources(ctx, client, acct, region, st, scanID) }},
		{mediaLiveListSignalMapsOp, func() (int, int, error) { return scanMLSignalMaps(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, ferr := phase.fn()
		if phases.addPhase(phase.op, t, i, ferr) {
			break
		}
	}
	return phases.result(st, acct, region)
}

func mlARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "medialive", region, acct, kind+"/"+id)
}

// mediaLiveRow builds the row of a listed MediaLive element.
func mediaLiveRow(acct *account, region, scanID, rtype, nativeID, name string, element any, tags *string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: tags, DiscoveredBy: scanID,
	}
}

// mediaLiveSkipOpNotDeployed makes list read as empty when its first page
// fails with isOpNotDeployed: MediaLive Anywhere (clusters, networks) is not
// offered in every MediaLive region, and those regions reject its list calls
// with HTTP 404. Once a page was read the endpoint serves the op, so a later
// failure is returned.
func mediaLiveSkipOpNotDeployed[Item any](list func(context.Context, *string) ([]Item, *string, error)) func(context.Context, *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := list(ctx, token)
		if err != nil && token == nil && isOpNotDeployed(err) {
			return nil, nil, nil
		}
		return items, next, err
	}
}

func scanMLChannels(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListChannelsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.ChannelSummary, *string, error) {
			out, err := client.ListChannels(ctx, &medialive.ListChannelsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Channels, out.NextToken, nil
		},
		func(c medialivetypes.ChannelSummary) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return nil, "channels have an empty Arn", sv(c.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveChannel, arn, cmp.Or(sv(c.Name), sv(c.Id)), c, mapTagsJSON(c.Tags)), "", ""
		})
}

// scanMLClusters stores the clusters and returns the ids of those stored,
// for the per-cluster phases.
func scanMLClusters(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		ids       []string
		noChildID skipTally
	)
	t, i, err := storeTokenList(ctx, st, acct, region, mediaLiveListClustersOp,
		mediaLiveSkipOpNotDeployed(func(ctx context.Context, token *string) ([]medialivetypes.DescribeClusterSummary, *string, error) {
			out, err := client.ListClusters(ctx, &medialive.ListClustersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Clusters, out.NextToken, nil
		}),
		func(c medialivetypes.DescribeClusterSummary) (*store.Resource, string, string) {
			arn, id := sv(c.Arn), sv(c.Id)
			if arn == "" {
				return nil, "clusters have an empty Arn, and their nodes and channel placement groups were not read either", id
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				ids = append(ids, id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveCluster, arn, cmp.Or(sv(c.Name), id), c, nil), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, mediaLiveListClustersOp, acct, region, &noChildID, "cluster(s)", "Id")
	}
	return ids, t, i, err
}

func scanMLChannelPlacementGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string, clusterIDs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mediaLiveListPlacementGroupsOp, nil, clusterIDs,
		func(ctx context.Context, cid string, token *string) ([]medialivetypes.DescribeChannelPlacementGroupSummary, *string, error) {
			out, err := client.ListChannelPlacementGroups(ctx, &medialive.ListChannelPlacementGroupsInput{ClusterId: &cid, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ChannelPlacementGroups, out.NextToken, nil
		},
		func(cid string, p medialivetypes.DescribeChannelPlacementGroupSummary) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "channel placement groups have an empty Arn (first names their cluster)", cid
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveChannelPlacementGroup, arn, cmp.Or(sv(p.Name), sv(p.Id)), p, nil), "", ""
		})
}

func scanMLAlarmTemplates(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListAlarmTemplatesOp,
		func(ctx context.Context, token *string) ([]medialivetypes.CloudWatchAlarmTemplateSummary, *string, error) {
			out, err := client.ListCloudWatchAlarmTemplates(ctx, &medialive.ListCloudWatchAlarmTemplatesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CloudWatchAlarmTemplates, out.NextToken, nil
		},
		func(a medialivetypes.CloudWatchAlarmTemplateSummary) (*store.Resource, string, string) {
			arn := sv(a.Arn)
			if arn == "" {
				return nil, "CloudWatch alarm templates have an empty Arn", sv(a.Id)
			}
			r := mediaLiveRow(acct, region, scanID, TypeMediaLiveCloudWatchAlarmTemplate, arn, cmp.Or(sv(a.Name), sv(a.Id)), a, mapTagsJSON(a.Tags))
			// AWS-supplied default templates carry an Id prefixed "aws-".
			r.ManagedByProvider = strings.HasPrefix(sv(a.Id), "aws-")
			return r, "", ""
		})
}

func scanMLAlarmTemplateGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListAlarmTemplateGroupsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.CloudWatchAlarmTemplateGroupSummary, *string, error) {
			out, err := client.ListCloudWatchAlarmTemplateGroups(ctx, &medialive.ListCloudWatchAlarmTemplateGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CloudWatchAlarmTemplateGroups, out.NextToken, nil
		},
		func(g medialivetypes.CloudWatchAlarmTemplateGroupSummary) (*store.Resource, string, string) {
			arn := sv(g.Arn)
			if arn == "" {
				return nil, "CloudWatch alarm template groups have an empty Arn", sv(g.Id)
			}
			r := mediaLiveRow(acct, region, scanID, TypeMediaLiveCloudWatchAlarmTemplateGroup, arn, cmp.Or(sv(g.Name), sv(g.Id)), g, mapTagsJSON(g.Tags))
			// AWS-supplied default groups carry an Id prefixed "aws-";
			// customer groups get a generated alphanumeric Id.
			r.ManagedByProvider = strings.HasPrefix(sv(g.Id), "aws-")
			return r, "", ""
		})
}

func scanMLEBTemplates(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListEBRuleTemplatesOp,
		func(ctx context.Context, token *string) ([]medialivetypes.EventBridgeRuleTemplateSummary, *string, error) {
			out, err := client.ListEventBridgeRuleTemplates(ctx, &medialive.ListEventBridgeRuleTemplatesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EventBridgeRuleTemplates, out.NextToken, nil
		},
		func(e medialivetypes.EventBridgeRuleTemplateSummary) (*store.Resource, string, string) {
			arn := sv(e.Arn)
			if arn == "" {
				return nil, "EventBridge rule templates have an empty Arn", sv(e.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveEventBridgeRuleTemplate, arn, cmp.Or(sv(e.Name), sv(e.Id)), e, mapTagsJSON(e.Tags)), "", ""
		})
}

func scanMLEBTemplateGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListEBRuleTemplateGroupsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.EventBridgeRuleTemplateGroupSummary, *string, error) {
			out, err := client.ListEventBridgeRuleTemplateGroups(ctx, &medialive.ListEventBridgeRuleTemplateGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EventBridgeRuleTemplateGroups, out.NextToken, nil
		},
		func(g medialivetypes.EventBridgeRuleTemplateGroupSummary) (*store.Resource, string, string) {
			arn := sv(g.Arn)
			if arn == "" {
				return nil, "EventBridge rule template groups have an empty Arn", sv(g.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveEventBridgeRuleTemplateGroup, arn, cmp.Or(sv(g.Name), sv(g.Id)), g, mapTagsJSON(g.Tags)), "", ""
		})
}

func scanMLInputs(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListInputsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.Input, *string, error) {
			out, err := client.ListInputs(ctx, &medialive.ListInputsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Inputs, out.NextToken, nil
		},
		func(in medialivetypes.Input) (*store.Resource, string, string) {
			arn := sv(in.Arn)
			if arn == "" {
				return nil, "inputs have an empty Arn", sv(in.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveInput, arn, cmp.Or(sv(in.Name), sv(in.Id)), in, mapTagsJSON(in.Tags)), "", ""
		})
}

func scanMLInputDevices(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListInputDevicesOp,
		func(ctx context.Context, token *string) ([]medialivetypes.InputDeviceSummary, *string, error) {
			out, err := client.ListInputDevices(ctx, &medialive.ListInputDevicesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InputDevices, out.NextToken, nil
		},
		func(d medialivetypes.InputDeviceSummary) (*store.Resource, string, string) {
			arn := sv(d.Arn)
			if arn == "" {
				return nil, "input devices have an empty Arn", sv(d.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveInputDevice, arn, cmp.Or(sv(d.Name), sv(d.Id)), d, mapTagsJSON(d.Tags)), "", ""
		})
}

// scanMLNodes lists the nodes of each cluster scanMLClusters stored
// (ListNodes requires a ClusterId). DescribeNodeSummary carries a real
// distinct node ARN, used directly as NativeID (edge-less).
func scanMLNodes(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string, clusterIDs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mediaLiveListNodesOp, nil, clusterIDs,
		func(ctx context.Context, cid string, token *string) ([]medialivetypes.DescribeNodeSummary, *string, error) {
			out, err := client.ListNodes(ctx, &medialive.ListNodesInput{ClusterId: &cid, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Nodes, out.NextToken, nil
		},
		func(cid string, n medialivetypes.DescribeNodeSummary) (*store.Resource, string, string) {
			arn := sv(n.Arn)
			if arn == "" {
				return nil, "nodes have an empty Arn (first names their cluster)", cid
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveNode, arn, cmp.Or(sv(n.Name), sv(n.Id)), n, nil), "", ""
		})
}

func scanMLReservations(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListReservationsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.Reservation, *string, error) {
			out, err := client.ListReservations(ctx, &medialive.ListReservationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Reservations, out.NextToken, nil
		},
		func(r medialivetypes.Reservation) (*store.Resource, string, string) {
			arn := sv(r.Arn)
			if arn == "" {
				return nil, "reservations have an empty Arn", sv(r.ReservationId)
			}
			row := mediaLiveRow(acct, region, scanID, TypeMediaLiveReservation, arn, cmp.Or(sv(r.Name), arn), r, mapTagsJSON(r.Tags))
			row.Status = nonEmptyPtr(string(r.State))
			return row, "", ""
		})
}

func scanMLInputSecurityGroups(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListInputSecurityGroupsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.InputSecurityGroup, *string, error) {
			out, err := client.ListInputSecurityGroups(ctx, &medialive.ListInputSecurityGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InputSecurityGroups, out.NextToken, nil
		},
		func(g medialivetypes.InputSecurityGroup) (*store.Resource, string, string) {
			arn := sv(g.Arn)
			if arn == "" {
				return nil, "input security groups have an empty Arn", sv(g.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveInputSecurityGroup, arn, sv(g.Id), g, mapTagsJSON(g.Tags)), "", ""
		})
}

// scanMLMultiplexes stores the multiplexes and returns the ids of those
// stored, for the multiplex program phase.
func scanMLMultiplexes(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		ids       []string
		noChildID skipTally
	)
	t, i, err := storeTokenList(ctx, st, acct, region, mediaLiveListMultiplexesOp,
		func(ctx context.Context, token *string) ([]medialivetypes.MultiplexSummary, *string, error) {
			out, err := client.ListMultiplexes(ctx, &medialive.ListMultiplexesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Multiplexes, out.NextToken, nil
		},
		func(m medialivetypes.MultiplexSummary) (*store.Resource, string, string) {
			arn, id := sv(m.Arn), sv(m.Id)
			if arn == "" {
				return nil, "multiplexes have an empty Arn, and their programs were not read either", id
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				ids = append(ids, id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveMultiplex, arn, cmp.Or(sv(m.Name), id), m, mapTagsJSON(m.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, mediaLiveListMultiplexesOp, acct, region, &noChildID, "multiplex(es)", "Id")
	}
	return ids, t, i, err
}

// scanMLMultiplexPrograms — MultiplexProgramSummary has no ARN; synth from
// (multiplexId, programName).
func scanMLMultiplexPrograms(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string, multiplexIDs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mediaLiveListMultiplexProgramsOp, []string{mediaLiveMultiplexProgramNotFoundError}, multiplexIDs,
		func(ctx context.Context, mid string, token *string) ([]medialivetypes.MultiplexProgramSummary, *string, error) {
			out, err := client.ListMultiplexPrograms(ctx, &medialive.ListMultiplexProgramsInput{MultiplexId: &mid, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MultiplexPrograms, out.NextToken, nil
		},
		func(mid string, p medialivetypes.MultiplexProgramSummary) (*store.Resource, string, string) {
			name := sv(p.ProgramName)
			if name == "" {
				return nil, "multiplex programs have an empty ProgramName (first names their multiplex)", mid
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveMultiplexProgram, mlARN(acct.partition(), region, acct.ID, "multiplexprogram", mid+"/"+name), name, p, nil), "", ""
		})
}

func scanMLNetworks(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListNetworksOp,
		mediaLiveSkipOpNotDeployed(func(ctx context.Context, token *string) ([]medialivetypes.DescribeNetworkSummary, *string, error) {
			out, err := client.ListNetworks(ctx, &medialive.ListNetworksInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Networks, out.NextToken, nil
		}),
		func(n medialivetypes.DescribeNetworkSummary) (*store.Resource, string, string) {
			arn := sv(n.Arn)
			if arn == "" {
				return nil, "networks have an empty Arn", sv(n.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveNetwork, arn, cmp.Or(sv(n.Name), sv(n.Id)), n, nil), "", ""
		})
}

func scanMLSdiSources(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListSdiSourcesOp,
		func(ctx context.Context, token *string) ([]medialivetypes.SdiSourceSummary, *string, error) {
			out, err := client.ListSdiSources(ctx, &medialive.ListSdiSourcesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SdiSources, out.NextToken, nil
		},
		func(s medialivetypes.SdiSourceSummary) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return nil, "SDI sources have an empty Arn", sv(s.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveSdiSource, arn, cmp.Or(sv(s.Name), sv(s.Id)), s, nil), "", ""
		})
}

func scanMLSignalMaps(ctx context.Context, client mediaLiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mediaLiveListSignalMapsOp,
		func(ctx context.Context, token *string) ([]medialivetypes.SignalMapSummary, *string, error) {
			out, err := client.ListSignalMaps(ctx, &medialive.ListSignalMapsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SignalMaps, out.NextToken, nil
		},
		func(s medialivetypes.SignalMapSummary) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return nil, "signal maps have an empty Arn", sv(s.Id)
			}
			return mediaLiveRow(acct, region, scanID, TypeMediaLiveSignalMap, arn, cmp.Or(sv(s.Name), sv(s.Id)), s, mapTagsJSON(s.Tags)), "", ""
		})
}
