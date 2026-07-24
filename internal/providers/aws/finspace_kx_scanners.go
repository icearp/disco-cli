package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/finspace"
)

func init() {
	registerType(restype.Descriptor{Type: TypeFinspaceKxEnvironment, Service: "finspace"})
	registerType(restype.Descriptor{Type: TypeFinspaceKxCluster, Service: "finspace"})
	registerType(restype.Descriptor{Type: TypeFinspaceKxDatabase, Service: "finspace"})
	registerType(restype.Descriptor{Type: TypeFinspaceKxDataview, Service: "finspace"})
	registerType(restype.Descriptor{Type: TypeFinspaceKxScalingGroup, Service: "finspace"})
	registerType(restype.Descriptor{Type: TypeFinspaceKxUser, Service: "finspace"})
	registerType(restype.Descriptor{Type: TypeFinspaceKxVolume, Service: "finspace"})
	registerService(serviceEntry{
		name: "aws:finspace",
		fn:   scanFinspaceKx,
	})
}

type finspaceKxAPI interface {
	ListKxEnvironments(context.Context, *finspace.ListKxEnvironmentsInput, ...func(*finspace.Options)) (*finspace.ListKxEnvironmentsOutput, error)
	ListKxClusters(context.Context, *finspace.ListKxClustersInput, ...func(*finspace.Options)) (*finspace.ListKxClustersOutput, error)
	ListKxDatabases(context.Context, *finspace.ListKxDatabasesInput, ...func(*finspace.Options)) (*finspace.ListKxDatabasesOutput, error)
	ListKxDataviews(context.Context, *finspace.ListKxDataviewsInput, ...func(*finspace.Options)) (*finspace.ListKxDataviewsOutput, error)
	ListKxScalingGroups(context.Context, *finspace.ListKxScalingGroupsInput, ...func(*finspace.Options)) (*finspace.ListKxScalingGroupsOutput, error)
	ListKxUsers(context.Context, *finspace.ListKxUsersInput, ...func(*finspace.Options)) (*finspace.ListKxUsersOutput, error)
	ListKxVolumes(context.Context, *finspace.ListKxVolumesInput, ...func(*finspace.Options)) (*finspace.ListKxVolumesOutput, error)
}

// scanFinspaceKx discovers FinSpace Managed kdb (kx) resources: parent kdb
// environment plus its clusters, databases, dataviews, scaling groups, users,
// and volumes. Child summaries carry no AWS-issued ARN (except KxUser), so
// NativeID synthesizes as `{envARN}/<kind>/{id}` (dominant child→parent
// shape); resolvers recover the parent by trimming the segment.
func scanFinspaceKx(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := finspace.NewFromConfig(acct.cfg, func(o *finspace.Options) { o.Region = region })
	return scanFinspaceKxEntities(ctx, client, acct, region, st, scanID)
}

func scanFinspaceKxEntities(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	type envRef struct{ arn, id string }
	var envs []envRef
	var batch []*store.Resource

	pager := finspace.NewListKxEnvironmentsPaginator(client, &finspace.ListKxEnvironmentsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// Per-region feature gap: FinSpace is deployed in a subset of regions.
			if isAccessDeniedWithMessage(err, "cannot access API in this region") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "finspace:ListKxEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("finspace:ListKxEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			arn := sv(e.EnvironmentArn)
			id := sv(e.EnvironmentId)
			if arn == "" || id == "" {
				continue
			}
			envs = append(envs, envRef{arn: arn, id: id})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFinspaceKxEnvironment, NativeID: arn,
				Name: e.Name, Region: &region, Status: sp(string(e.Status)),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}

	for _, env := range envs {
		batch = append(batch, scanFinspaceKxChildren(ctx, client, acct, region, st, scanID, env.arn, env.id)...)
	}
	return upsertBatch(st, batch, "finspace kx-resources")
}

// scanFinspaceKxChildren fans out every per-environment list op. Per-environment
// AccessDenied/not-found/validation errors are tolerated (environment may be
// mid-creation or partially permissioned) — never abort the scan.
func scanFinspaceKxChildren(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envARN, envID string) []*store.Resource {
	var out []*store.Resource
	out = append(out, listKxClusters(ctx, client, acct, region, st, scanID, envARN, envID)...)
	dbBatch, dbNames := listKxDatabases(ctx, client, acct, region, st, scanID, envARN, envID)
	out = append(out, dbBatch...)
	for _, db := range dbNames {
		out = append(out, listKxDataviews(ctx, client, acct, region, st, scanID, envARN, envID, db)...)
	}
	out = append(out, listKxScalingGroups(ctx, client, acct, region, st, scanID, envARN, envID)...)
	out = append(out, listKxUsers(ctx, client, acct, region, st, scanID, envID)...)
	out = append(out, listKxVolumes(ctx, client, acct, region, st, scanID, envARN, envID)...)
	return out
}

func (a *account) kxResource(rtype, nativeID string, name *string, region, scanID string, status *string, attrs any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: a.ID, AccountName: &a.Name,
		Type: rtype, NativeID: nativeID,
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}

func listKxClusters(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envARN, envID string) []*store.Resource {
	var out []*store.Resource
	var token *string
	for {
		res, err := client.ListKxClusters(ctx, &finspace.ListKxClustersInput{EnvironmentId: &envID, NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "finspace:ListKxClusters", acct.ID, region, err)
			}
			break
		}
		for _, c := range res.KxClusterSummaries {
			if name := sv(c.ClusterName); name != "" {
				out = append(out, acct.kxResource(TypeFinspaceKxCluster, envARN+"/cluster/"+name, c.ClusterName, region, scanID, sp(string(c.Status)), c))
			}
		}
		if res.NextToken == nil || *res.NextToken == "" {
			break
		}
		token = res.NextToken
	}
	return out
}

func listKxDatabases(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envARN, envID string) ([]*store.Resource, []string) {
	var out []*store.Resource
	var names []string
	pager := finspace.NewListKxDatabasesPaginator(client, &finspace.ListKxDatabasesInput{EnvironmentId: &envID})
	for pager.HasMorePages() {
		res, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "finspace:ListKxDatabases", acct.ID, region, err)
			}
			break
		}
		for _, d := range res.KxDatabases {
			if name := sv(d.DatabaseName); name != "" {
				names = append(names, name)
				out = append(out, acct.kxResource(TypeFinspaceKxDatabase, envARN+"/database/"+name, d.DatabaseName, region, scanID, nil, d))
			}
		}
	}
	return out, names
}

func listKxDataviews(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envARN, envID, dbName string) []*store.Resource {
	var out []*store.Resource
	db := dbName
	pager := finspace.NewListKxDataviewsPaginator(client, &finspace.ListKxDataviewsInput{EnvironmentId: &envID, DatabaseName: &db})
	for pager.HasMorePages() {
		res, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "finspace:ListKxDataviews", acct.ID, region, err)
			}
			break
		}
		for _, v := range res.KxDataviews {
			if name := sv(v.DataviewName); name != "" {
				out = append(out, acct.kxResource(TypeFinspaceKxDataview, envARN+"/database/"+dbName+"/dataview/"+name, v.DataviewName, region, scanID, sp(string(v.Status)), v))
			}
		}
	}
	return out
}

func listKxScalingGroups(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envARN, envID string) []*store.Resource {
	var out []*store.Resource
	pager := finspace.NewListKxScalingGroupsPaginator(client, &finspace.ListKxScalingGroupsInput{EnvironmentId: &envID})
	for pager.HasMorePages() {
		res, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "finspace:ListKxScalingGroups", acct.ID, region, err)
			}
			break
		}
		for _, g := range res.ScalingGroups {
			if name := sv(g.ScalingGroupName); name != "" {
				out = append(out, acct.kxResource(TypeFinspaceKxScalingGroup, envARN+"/scaling-group/"+name, g.ScalingGroupName, region, scanID, sp(string(g.Status)), g))
			}
		}
	}
	return out
}

// listKxUsers reads kdb users. KxUser carries a real UserArn used as NativeID.
func listKxUsers(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envID string) []*store.Resource {
	var out []*store.Resource
	var token *string
	for {
		res, err := client.ListKxUsers(ctx, &finspace.ListKxUsersInput{EnvironmentId: &envID, NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "finspace:ListKxUsers", acct.ID, region, err)
			}
			break
		}
		for _, u := range res.Users {
			if arn := sv(u.UserArn); arn != "" {
				out = append(out, acct.kxResource(TypeFinspaceKxUser, arn, u.UserName, region, scanID, nil, u))
			}
		}
		if res.NextToken == nil || *res.NextToken == "" {
			break
		}
		token = res.NextToken
	}
	return out
}

func listKxVolumes(ctx context.Context, client finspaceKxAPI, acct *account, region string, st *store.Store, scanID, envARN, envID string) []*store.Resource {
	var out []*store.Resource
	var token *string
	for {
		res, err := client.ListKxVolumes(ctx, &finspace.ListKxVolumesInput{EnvironmentId: &envID, NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "finspace:ListKxVolumes", acct.ID, region, err)
			}
			break
		}
		for _, v := range res.KxVolumeSummaries {
			if name := sv(v.VolumeName); name != "" {
				out = append(out, acct.kxResource(TypeFinspaceKxVolume, envARN+"/volume/"+name, v.VolumeName, region, scanID, sp(string(v.Status)), v))
			}
		}
		if res.NextToken == nil || *res.NextToken == "" {
			break
		}
		token = res.NextToken
	}
	return out
}
