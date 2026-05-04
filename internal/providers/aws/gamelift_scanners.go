package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
)

func init() {
	registerService(serviceEntry{
		name: "aws:gamelift",
		fn:   scanGameLift,
		emits: []coverage.TypeDecl{
			{Service: "gamelift", DiscoType: TypeGameLiftAlias},
			{Service: "gamelift", DiscoType: TypeGameLiftBuild},
			{Service: "gamelift", DiscoType: TypeGameLiftContainerFleet},
			{Service: "gamelift", DiscoType: TypeGameLiftContainerGroupDefinition},
			{Service: "gamelift", DiscoType: TypeGameLiftFleet},
			{Service: "gamelift", DiscoType: TypeGameLiftGameServerGroup},
			{Service: "gamelift", DiscoType: TypeGameLiftGameSessionQueue},
			{Service: "gamelift", DiscoType: TypeGameLiftLocation},
			{Service: "gamelift", DiscoType: TypeGameLiftMatchmakingConfiguration},
			{Service: "gamelift", DiscoType: TypeGameLiftMatchmakingRuleSet},
			{Service: "gamelift", DiscoType: TypeGameLiftScript},
		},
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
}

func scanGameLift(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := gamelift.NewFromConfig(acct.cfg, func(o *gamelift.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGLAliases(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLBuilds(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLContainerFleets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLContainerGroupDefs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLFleets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLGameServerGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLGameSessionQueues(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLLocations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLMatchmakingConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLMatchmakingRuleSets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGLScripts(ctx, client, acct, region, st, scanID) },
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
			// GameLift Containers is not deployed in every region; AWS
			// returns UnsupportedRegionException there. Silent-skip.
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

func scanGLFleets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// ListFleets returns IDs only — fan out via DescribeFleetAttributes (50/page).
	var fleetIDs []string
	pager := gamelift.NewListFleetsPaginator(client, &gamelift.ListFleetsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "gamelift:ListFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:ListFleets: %w", perr)
		}
		fleetIDs = append(fleetIDs, out.FleetIds...)
	}
	var batch []*store.Resource
	for i := 0; i < len(fleetIDs); i += 50 {
		end := i + 50
		if end > len(fleetIDs) {
			end = len(fleetIDs)
		}
		out, derr := client.DescribeFleetAttributes(ctx, &gamelift.DescribeFleetAttributesInput{FleetIds: fleetIDs[i:end]})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "gamelift:DescribeFleetAttributes", acct.ID, region, derr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("gamelift:DescribeFleetAttributes: %w", derr)
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
		}
	}
	return upsertBatch(st, batch, "gamelift fleets")
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
