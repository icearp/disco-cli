package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGameLiftAlias, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftBuild, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftCompute, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftContainerFleet, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftContainerGroupDefinition, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftFleet, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftGameServerGroup, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftGameSessionQueue, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftLocation, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftMatchmakingConfiguration, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftMatchmakingRuleSet, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftScalingPolicy, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftScript, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftVpcPeeringConnection, Service: "gamelift"})
	registerService(serviceEntry{
		name: "aws:gamelift",
		fn:   scanGameLift,
	})
}

type gameLiftAPI interface {
	ListAliases(context.Context, *gamelift.ListAliasesInput, ...func(*gamelift.Options)) (*gamelift.ListAliasesOutput, error)
	ListBuilds(context.Context, *gamelift.ListBuildsInput, ...func(*gamelift.Options)) (*gamelift.ListBuildsOutput, error)
	ListContainerFleets(context.Context, *gamelift.ListContainerFleetsInput, ...func(*gamelift.Options)) (*gamelift.ListContainerFleetsOutput, error)
	ListContainerGroupDefinitions(context.Context, *gamelift.ListContainerGroupDefinitionsInput, ...func(*gamelift.Options)) (*gamelift.ListContainerGroupDefinitionsOutput, error)
	ListFleets(context.Context, *gamelift.ListFleetsInput, ...func(*gamelift.Options)) (*gamelift.ListFleetsOutput, error)
	DescribeFleetAttributes(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	ListGameServerGroups(context.Context, *gamelift.ListGameServerGroupsInput, ...func(*gamelift.Options)) (*gamelift.ListGameServerGroupsOutput, error)
	DescribeGameSessionQueues(context.Context, *gamelift.DescribeGameSessionQueuesInput, ...func(*gamelift.Options)) (*gamelift.DescribeGameSessionQueuesOutput, error)
	ListLocations(context.Context, *gamelift.ListLocationsInput, ...func(*gamelift.Options)) (*gamelift.ListLocationsOutput, error)
	DescribeMatchmakingConfigurations(context.Context, *gamelift.DescribeMatchmakingConfigurationsInput, ...func(*gamelift.Options)) (*gamelift.DescribeMatchmakingConfigurationsOutput, error)
	DescribeMatchmakingRuleSets(context.Context, *gamelift.DescribeMatchmakingRuleSetsInput, ...func(*gamelift.Options)) (*gamelift.DescribeMatchmakingRuleSetsOutput, error)
	ListScripts(context.Context, *gamelift.ListScriptsInput, ...func(*gamelift.Options)) (*gamelift.ListScriptsOutput, error)
	ListCompute(context.Context, *gamelift.ListComputeInput, ...func(*gamelift.Options)) (*gamelift.ListComputeOutput, error)
	DescribeScalingPolicies(context.Context, *gamelift.DescribeScalingPoliciesInput, ...func(*gamelift.Options)) (*gamelift.DescribeScalingPoliciesOutput, error)
	DescribeVpcPeeringConnections(context.Context, *gamelift.DescribeVpcPeeringConnectionsInput, ...func(*gamelift.Options)) (*gamelift.DescribeVpcPeeringConnectionsOutput, error)
}

func scanGameLift(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := gamelift.NewFromConfig(acct.cfg, func(o *gamelift.Options) { o.Region = region })
	return scanGameLiftIn(ctx, client, acct, region, st, scanID)
}

// scanGameLiftIn runs every phase even when one fails, so one phase's error
// costs no other phase its rows. The fleet children phases run last, over the
// fleets the fleet phase stored.
//
// Every phase calls the same endpoint, so a first phase that cannot reach it
// (isEndpointUnreachable) is returned alone and no later phase runs: they
// would fail the same way, and the dispatcher must see the bare error to
// classify it (NXDOMAIN in a region without GameLift is region-unavailable).
// A later phase's transient error is warned about here rather than returned:
// the dispatcher classifies a joined error by any member, so one throttled
// phase would make the whole service a transient skip, discarding the other
// phases' counts and downgrading their hard errors.
func scanGameLiftIn(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		fleets []gltypes.FleetAttributes
		errs   []error
	)
	for n, phase := range []struct {
		name string
		run  func() (int, int, error)
	}{
		{"gamelift:ListAliases", func() (int, int, error) { return scanGLAliases(ctx, client, acct, region, st, scanID) }},
		{"gamelift:ListBuilds", func() (int, int, error) { return scanGLBuilds(ctx, client, acct, region, st, scanID) }},
		{"gamelift:ListContainerFleets", func() (int, int, error) { return scanGLContainerFleets(ctx, client, acct, region, st, scanID) }},
		{"gamelift:ListContainerGroupDefinitions", func() (int, int, error) {
			return scanGLContainerGroupDefs(ctx, client, acct, region, st, scanID)
		}},
		{"gamelift:ListFleets", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			fleets, t, i, err = scanGLFleets(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"gamelift:ListGameServerGroups", func() (int, int, error) { return scanGLGameServerGroups(ctx, client, acct, region, st, scanID) }},
		{"gamelift:DescribeGameSessionQueues", func() (int, int, error) {
			return scanGLGameSessionQueues(ctx, client, acct, region, st, scanID)
		}},
		{"gamelift:ListLocations", func() (int, int, error) { return scanGLLocations(ctx, client, acct, region, st, scanID) }},
		{"gamelift:DescribeMatchmakingConfigurations", func() (int, int, error) {
			return scanGLMatchmakingConfigs(ctx, client, acct, region, st, scanID)
		}},
		{"gamelift:DescribeMatchmakingRuleSets", func() (int, int, error) {
			return scanGLMatchmakingRuleSets(ctx, client, acct, region, st, scanID)
		}},
		{"gamelift:ListScripts", func() (int, int, error) { return scanGLScripts(ctx, client, acct, region, st, scanID) }},
		{"gamelift:ListCompute", func() (int, int, error) { return scanGLComputes(ctx, client, acct, region, st, scanID, fleets) }},
		{"gamelift:DescribeScalingPolicies", func() (int, int, error) {
			return scanGLScalingPolicies(ctx, client, acct, region, st, scanID, fleets)
		}},
		{"gamelift:DescribeVpcPeeringConnections", func() (int, int, error) {
			return scanGLVpcPeeringConnections(ctx, client, acct, region, st, scanID, fleets)
		}},
	} {
		t, i, perr := phase.run()
		if n == 0 && isEndpointUnreachable(perr) {
			return t, i, perr
		}
		total += t
		inserted += i
		if isTransientNetworkError(perr) {
			_ = skipIfTransient(st, phase.name, acct.ID, region, perr)
			continue
		}
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

func scanGLAliases(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListAliasesPaginator(client, &gamelift.ListAliasesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListAliases", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListAliases: %w", perr)
		}
		for _, a := range out.Aliases {
			arn := sv(a.AliasArn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.AliasId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftAlias, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift aliases")
}

func scanGLBuilds(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListBuildsPaginator(client, &gamelift.ListBuildsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListBuilds", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListBuilds: %w", perr)
		}
		for _, b := range out.Builds {
			arn := sv(b.BuildArn)
			if arn == "" {
				continue
			}
			label := sv(b.Name)
			if label == "" {
				label = sv(b.BuildId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftBuild, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift builds")
}

func scanGLContainerFleets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListContainerFleetsPaginator(client, &gamelift.ListContainerFleetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// GameLift Containers isn't deployed in every region; AWS returns
			// UnsupportedRegionException there — silent-skip.
			if isAPIErrorCode(perr, "UnsupportedRegionException") {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListContainerFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListContainerFleets: %w", perr)
		}
		for _, f := range out.ContainerFleets {
			arn := sv(f.FleetArn)
			if arn == "" {
				continue
			}
			label := sv(f.FleetId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftContainerFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift container-fleets")
}

func scanGLContainerGroupDefs(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListContainerGroupDefinitionsPaginator(client, &gamelift.ListContainerGroupDefinitionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAPIErrorCode(perr, "UnsupportedRegionException") {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListContainerGroupDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListContainerGroupDefinitions: %w", perr)
		}
		for _, d := range out.ContainerGroupDefinitions {
			arn := sv(d.ContainerGroupDefinitionArn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftContainerGroupDefinition, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift container-group-defs")
}

// scanGLFleets stores every fleet and returns the stored fleets' attributes
// for the fleet children phases.
func scanGLFleets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) ([]gltypes.FleetAttributes, int, int, error) {
	// ListFleets returns IDs only — fan out via DescribeFleetAttributes (50/page).
	var fleetIDs []string
	pager := gamelift.NewListFleetsPaginator(client, &gamelift.ListFleetsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListFleets", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("gamelift:ListFleets: %w", perr)
		}
		fleetIDs = append(fleetIDs, out.FleetIds...)
	}
	var (
		batch  []*store.Resource
		fleets []gltypes.FleetAttributes
	)
	for i := 0; i < len(fleetIDs); i += 50 {
		end := i + 50
		if end > len(fleetIDs) {
			end = len(fleetIDs)
		}
		out, derr := client.DescribeFleetAttributes(ctx, &gamelift.DescribeFleetAttributesInput{FleetIds: fleetIDs[i:end]})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "gamelift:DescribeFleetAttributes", acct.ID, region, derr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("gamelift:DescribeFleetAttributes: %w", derr)
		}
		for _, f := range out.FleetAttributes {
			arn := sv(f.FleetArn)
			if arn == "" {
				continue
			}
			label := sv(f.Name)
			if label == "" {
				label = sv(f.FleetId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
			fleets = append(fleets, f)
		}
	}
	total, inserted, err := upsertBatch(st, batch, "gamelift fleets")
	if err != nil {
		return nil, 0, 0, err
	}
	return fleets, total, inserted, nil
}

// gameLiftFleetParents returns the fleets keep accepts as child fan-out
// parents, leaving out TERMINATED fleets, which FleetStatus documents as no
// longer existing.
func gameLiftFleetParents(fleets []gltypes.FleetAttributes, keep func(gltypes.FleetAttributes) bool) []childParent {
	var parents []childParent
	for _, f := range fleets {
		if f.Status == gltypes.FleetStatusTerminated || !keep(f) {
			continue
		}
		parents = append(parents, childParent{id: sv(f.FleetId), arn: sv(f.FleetArn)})
	}
	return parents
}

// scanGLComputes stores the computes registered to Anywhere fleets
// (RegisterCompute). The computes ListCompute returns for an EC2 fleet are its
// managed instances, which GameLift launches and terminates as the fleet
// scales, so EC2 fleets are not queried.
func scanGLComputes(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, fleets []gltypes.FleetAttributes) (int, int, error) {
	parents := gameLiftFleetParents(fleets, func(f gltypes.FleetAttributes) bool { return f.ComputeType == gltypes.ComputeTypeAnywhere })
	// ListCompute models no not-found error, so no fleet's error is skipped.
	return childFanOut(ctx, st, acct, region, "gamelift:ListCompute", parents, func(error) bool { return false },
		func(ctx context.Context, fleet childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := gamelift.NewListComputePaginator(client, &gamelift.ListComputeInput{FleetId: &fleet.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.ComputeList {
					arn := sv(c.ComputeArn)
					if arn == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGameLiftCompute, NativeID: arn,
						Name: c.ComputeName, Region: &region, Status: nonEmptyPtr(string(c.ComputeStatus)),
						CreatedAt: tp(c.CreationTime), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanGLScalingPolicies stores the scaling policies of every fleet but
// Anywhere fleets: the op documents EC2 fleets only, and a fleet reporting no
// ComputeType (the field is optional) is queried rather than assumed not EC2.
// A policy has no ARN; a fleet holds one policy per name (PutScalingPolicy),
// so the policy is keyed {fleetARN}/scaling-policy/{name}. Called without a
// Location, the op returns the policies of every fleet location, while
// PutScalingPolicy takes no location, so only the home-region entry (or one
// naming no location) is kept.
func scanGLScalingPolicies(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, fleets []gltypes.FleetAttributes) (int, int, error) {
	parents := gameLiftFleetParents(fleets, func(f gltypes.FleetAttributes) bool { return f.ComputeType != gltypes.ComputeTypeAnywhere })
	return childFanOut(ctx, st, acct, region, "gamelift:DescribeScalingPolicies", parents, isGameLiftNotFound,
		func(ctx context.Context, fleet childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := gamelift.NewDescribeScalingPoliciesPaginator(client, &gamelift.DescribeScalingPoliciesInput{FleetId: &fleet.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, p := range out.ScalingPolicies {
					name := sv(p.Name)
					if name == "" || p.Status == gltypes.ScalingStatusTypeDeleted {
						continue
					}
					if loc := sv(p.Location); loc != "" && loc != region {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGameLiftScalingPolicy, NativeID: fleet.arn + "/scaling-policy/" + name,
						Name: &name, Region: &region, Status: nonEmptyPtr(string(p.Status)),
						AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// isGameLiftNotFound matches a fleet deleted between DescribeFleetAttributes
// and the child call.
func isGameLiftNotFound(err error) bool {
	return isAPIErrorCode(err, "NotFoundException")
}

// scanGLVpcPeeringConnections stores GameLift's record of each peering
// connection between a fleet's GameLift-managed VPC and a VPC of this account,
// keyed {fleetARN}/vpc-peering-connection/{id}. The connection is not keyed as
// the EC2 peering connection: ec2:DescribeVpcPeeringConnections stores that as
// aws:ec2:vpc-peering-connection, and sharing its NativeID would merge the two
// rows into one version chain. A record naming its fleet only by FleetId gets
// the fleet ARN built from the id, the same shape the fleet row is keyed by. A
// pending connection with no id yet is skipped. The op lists the whole account's connections at once; each is
// linked under its fleet only when the fleet phase stored that fleet.
func scanGLVpcPeeringConnections(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, fleets []gltypes.FleetAttributes) (int, int, error) {
	out, err := client.DescribeVpcPeeringConnections(ctx, &gamelift.DescribeVpcPeeringConnectionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "gamelift:DescribeVpcPeeringConnections", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("gamelift:DescribeVpcPeeringConnections: %w", err)
	}
	stored := make(map[string]bool, len(fleets))
	for _, f := range fleets {
		stored[sv(f.FleetArn)] = true
	}
	var (
		batch     []*store.Resource
		fleetARNs []string // fleetARNs[i] is batch[i]'s fleet
	)
	for _, c := range out.VpcPeeringConnections {
		fleetARN, id := sv(c.FleetArn), sv(c.VpcPeeringConnectionId)
		if fleetARN == "" && sv(c.FleetId) != "" {
			fleetARN = gameliftFleetARN(region, acct.ID, sv(c.FleetId))
		}
		if fleetARN == "" || id == "" {
			continue
		}
		var status *string
		if c.Status != nil {
			status = nonEmptyPtr(sv(c.Status.Code))
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeGameLiftVpcPeeringConnection, NativeID: fleetARN + "/vpc-peering-connection/" + id,
			Name: &id, Region: &region, Status: status, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
		})
		fleetARNs = append(fleetARNs, fleetARN)
	}
	total, inserted, err := upsertBatch(st, batch, "gamelift vpc-peering-connections")
	if err != nil {
		return 0, 0, err
	}
	var pairs [][2]string
	for i, r := range batch {
		if stored[fleetARNs[i]] {
			pairs = append(pairs, [2]string{r.ID, store.ResourceID("aws", acct.ID, fleetARNs[i])})
		}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure gamelift vpc-peering-connections: %w", err)
	}
	return total, inserted, nil
}

func scanGLGameServerGroups(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListGameServerGroupsPaginator(client, &gamelift.ListGameServerGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListGameServerGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListGameServerGroups: %w", perr)
		}
		for _, g := range out.GameServerGroups {
			arn := sv(g.GameServerGroupArn)
			if arn == "" {
				continue
			}
			label := sv(g.GameServerGroupName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftGameServerGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift game-server-groups")
}

func scanGLGameSessionQueues(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewDescribeGameSessionQueuesPaginator(client, &gamelift.DescribeGameSessionQueuesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:DescribeGameSessionQueues", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:DescribeGameSessionQueues: %w", perr)
		}
		for _, q := range out.GameSessionQueues {
			arn := sv(q.GameSessionQueueArn)
			if arn == "" {
				continue
			}
			label := sv(q.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftGameSessionQueue, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift game-session-queues")
}

func scanGLLocations(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListLocationsPaginator(client, &gamelift.ListLocationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListLocations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListLocations: %w", perr)
		}
		for _, l := range out.Locations {
			arn := sv(l.LocationArn)
			if arn == "" {
				continue
			}
			label := sv(l.LocationName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftLocation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift locations")
}

func scanGLMatchmakingConfigs(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewDescribeMatchmakingConfigurationsPaginator(client, &gamelift.DescribeMatchmakingConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAPIErrorCode(perr, "UnsupportedRegionException") {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:DescribeMatchmakingConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:DescribeMatchmakingConfigurations: %w", perr)
		}
		for _, c := range out.Configurations {
			arn := sv(c.ConfigurationArn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftMatchmakingConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift matchmaking-configs")
}

func scanGLMatchmakingRuleSets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewDescribeMatchmakingRuleSetsPaginator(client, &gamelift.DescribeMatchmakingRuleSetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAPIErrorCode(perr, "UnsupportedRegionException") {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:DescribeMatchmakingRuleSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:DescribeMatchmakingRuleSets: %w", perr)
		}
		for _, r := range out.RuleSets {
			arn := sv(r.RuleSetArn)
			if arn == "" {
				continue
			}
			label := sv(r.RuleSetName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftMatchmakingRuleSet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift matchmaking-rule-sets")
}

func scanGLScripts(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gamelift.NewListScriptsPaginator(client, &gamelift.ListScriptsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListScripts", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListScripts: %w", perr)
		}
		for _, s := range out.Scripts {
			arn := sv(s.ScriptArn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.ScriptId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftScript, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gamelift scripts")
}
