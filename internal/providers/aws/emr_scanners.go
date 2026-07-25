package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/emr"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEMRCluster, Service: "emr"})
	registerType(restype.Descriptor{Type: TypeEMRInstanceFleet, Service: "emr", Upstream: "AWS::EMR::InstanceFleetConfig"})
	registerType(restype.Descriptor{Type: TypeEMRInstanceGroup, Service: "emr", Upstream: "AWS::EMR::InstanceGroupConfig"})
	registerType(restype.Descriptor{Type: TypeEMRSecurityConfig, Service: "emr", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEMRStep, Service: "emr"})
	registerType(restype.Descriptor{Type: TypeEMRStudio, Service: "emr"})
	registerType(restype.Descriptor{Type: TypeEMRStudioSessionMapping, Service: "emr"})
	registerService(serviceEntry{
		name: "aws:emr",
		fn:   scanEMR,
	})
}

type emrAPI interface {
	ListClusters(context.Context, *emr.ListClustersInput, ...func(*emr.Options)) (*emr.ListClustersOutput, error)
	DescribeCluster(context.Context, *emr.DescribeClusterInput, ...func(*emr.Options)) (*emr.DescribeClusterOutput, error)
	ListInstanceFleets(context.Context, *emr.ListInstanceFleetsInput, ...func(*emr.Options)) (*emr.ListInstanceFleetsOutput, error)
	ListInstanceGroups(context.Context, *emr.ListInstanceGroupsInput, ...func(*emr.Options)) (*emr.ListInstanceGroupsOutput, error)
	ListSecurityConfigurations(context.Context, *emr.ListSecurityConfigurationsInput, ...func(*emr.Options)) (*emr.ListSecurityConfigurationsOutput, error)
	ListSteps(context.Context, *emr.ListStepsInput, ...func(*emr.Options)) (*emr.ListStepsOutput, error)
	ListStudios(context.Context, *emr.ListStudiosInput, ...func(*emr.Options)) (*emr.ListStudiosOutput, error)
	ListStudioSessionMappings(context.Context, *emr.ListStudioSessionMappingsInput, ...func(*emr.Options)) (*emr.ListStudioSessionMappingsOutput, error)
}

func emrARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:elasticmapreduce:%s:%s:%s/%s", region, acct, kind, id)
}

func scanEMR(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := emr.NewFromConfig(acct.cfg, func(o *emr.Options) { o.Region = region })

	type clusterRef struct {
		id, arn string
	}
	pager := emr.NewListClustersPaginator(client, &emr.ListClustersInput{})
	var batch []*store.Resource
	var clusters []clusterRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListClusters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListClusters: %w", perr)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ClusterArn)
			id := sv(c.Id)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = id
			}
			if id != "" {
				clusters = append(clusters, clusterRef{id: id, arn: arn})
			}
			attrsJSON := mustJSON(c)
			idLocal := id
			if dout, derr := client.DescribeCluster(ctx, &emr.DescribeClusterInput{ClusterId: &idLocal}); derr == nil && dout.Cluster != nil {
				attrsJSON = mustJSON(dout.Cluster)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRCluster, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "emr clusters")
	if err != nil {
		return 0, 0, err
	}
	total += t
	inserted += i

	// Per-cluster: instance fleets, groups, steps.
	for _, cl := range clusters {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) {
				return scanEMRInstanceFleets(ctx, client, acct, region, st, scanID, cl.id, cl.arn)
			},
			func() (int, int, error) {
				return scanEMRInstanceGroups(ctx, client, acct, region, st, scanID, cl.id, cl.arn)
			},
			func() (int, int, error) {
				return scanEMRSteps(ctx, client, acct, region, st, scanID, cl.id, cl.arn)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}

	// Top-level: security-configurations.
	t, i, ferr := scanEMRSecurityConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Studios + per-studio session-mappings.
	studioIDs, t, i, ferr := scanEMRStudios(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	for _, sid := range studioIDs {
		t, i, perr := scanEMRStudioSessionMappings(ctx, client, acct, region, st, scanID, sid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanEMRInstanceFleets(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, clusterID, clusterARN string) (int, int, error) {
	cid := clusterID
	pager := emr.NewListInstanceFleetsPaginator(client, &emr.ListInstanceFleetsInput{ClusterId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) || isAPIErrorCode(perr, "InvalidRequestException") {
				_ = skipIfAccessDenied(st, "emr:ListInstanceFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListInstanceFleets: %w", perr)
		}
		for _, f := range out.InstanceFleets {
			id := sv(f.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/instance-fleet/%s", clusterARN, id)
			label := sv(f.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRInstanceFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr instance-fleets")
}

func scanEMRInstanceGroups(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, clusterID, clusterARN string) (int, int, error) {
	cid := clusterID
	pager := emr.NewListInstanceGroupsPaginator(client, &emr.ListInstanceGroupsInput{ClusterId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) || isAPIErrorCode(perr, "InvalidRequestException") {
				_ = skipIfAccessDenied(st, "emr:ListInstanceGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListInstanceGroups: %w", perr)
		}
		for _, g := range out.InstanceGroups {
			id := sv(g.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/instance-group/%s", clusterARN, id)
			label := sv(g.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRInstanceGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr instance-groups")
}

func scanEMRSteps(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, clusterID, clusterARN string) (int, int, error) {
	cid := clusterID
	pager := emr.NewListStepsPaginator(client, &emr.ListStepsInput{ClusterId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListSteps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListSteps: %w", perr)
		}
		for _, s := range out.Steps {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/step/%s", clusterARN, id)
			label := sv(s.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRStep, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr steps")
}

func scanEMRSecurityConfigs(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := emr.NewListSecurityConfigurationsPaginator(client, &emr.ListSecurityConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListSecurityConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListSecurityConfigurations: %w", perr)
		}
		for _, sc := range out.SecurityConfigurations {
			name := sv(sc.Name)
			if name == "" {
				continue
			}
			arn := emrARN(region, acct.ID, "security-configuration", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRSecurityConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr security-configurations")
}

func scanEMRStudios(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := emr.NewListStudiosPaginator(client, &emr.ListStudiosInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListStudios", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("emr:ListStudios: %w", perr)
		}
		for _, s := range out.Studios {
			id := sv(s.StudioId)
			if id == "" {
				continue
			}
			arn := emrARN(region, acct.ID, "studio", id)
			label := sv(s.Name)
			if label == "" {
				label = id
			}
			ids = append(ids, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRStudio, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "emr studios")
	return ids, t, i, err
}

func scanEMRStudioSessionMappings(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, studioID string) (int, int, error) {
	sid := studioID
	pager := emr.NewListStudioSessionMappingsPaginator(client, &emr.ListStudioSessionMappingsInput{StudioId: &sid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListStudioSessionMappings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListStudioSessionMappings: %w", perr)
		}
		for _, sm := range out.SessionMappings {
			iid := sv(sm.IdentityId)
			if iid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:elasticmapreduce:%s:%s:studio/%s/identity/%s", region, acct.ID, studioID, iid)
			label := sv(sm.IdentityName)
			if label == "" {
				label = iid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRStudioSessionMapping, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sm), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr studio-session-mappings")
}
