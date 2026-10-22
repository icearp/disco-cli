package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectUser, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectUserHierarchyGroup, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectUserHierarchyStructure, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectSecurityProfile, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectPredefinedAttribute, Service: "connect"})
}

// connectUsersAPI is the narrow surface used by the Users family.
type connectUsersAPI interface {
	ListUsers(context.Context, *connect.ListUsersInput, ...func(*connect.Options)) (*connect.ListUsersOutput, error)
	DescribeUser(context.Context, *connect.DescribeUserInput, ...func(*connect.Options)) (*connect.DescribeUserOutput, error)
	ListUserHierarchyGroups(context.Context, *connect.ListUserHierarchyGroupsInput, ...func(*connect.Options)) (*connect.ListUserHierarchyGroupsOutput, error)
	DescribeUserHierarchyGroup(context.Context, *connect.DescribeUserHierarchyGroupInput, ...func(*connect.Options)) (*connect.DescribeUserHierarchyGroupOutput, error)
	DescribeUserHierarchyStructure(context.Context, *connect.DescribeUserHierarchyStructureInput, ...func(*connect.Options)) (*connect.DescribeUserHierarchyStructureOutput, error)
	ListSecurityProfiles(context.Context, *connect.ListSecurityProfilesInput, ...func(*connect.Options)) (*connect.ListSecurityProfilesOutput, error)
	DescribeSecurityProfile(context.Context, *connect.DescribeSecurityProfileInput, ...func(*connect.Options)) (*connect.DescribeSecurityProfileOutput, error)
	ListPredefinedAttributes(context.Context, *connect.ListPredefinedAttributesInput, ...func(*connect.Options)) (*connect.ListPredefinedAttributesOutput, error)
	DescribePredefinedAttribute(context.Context, *connect.DescribePredefinedAttributeInput, ...func(*connect.Options)) (*connect.DescribePredefinedAttributeOutput, error)
}

// scanConnectUsers runs the Users-family phases per instance.
func scanConnectUsers(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	return connectRunPhases(st, acct, region,
		connectPhase{"connect users", func() (int, int, error) {
			return scanConnectUserList(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect user hierarchy groups", func() (int, int, error) {
			return scanConnectUserHierarchyGroups(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect user hierarchy structures", func() (int, int, error) {
			return scanConnectUserHierarchyStructures(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect security profiles", func() (int, int, error) {
			return scanConnectSecurityProfiles(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect predefined attributes", func() (int, int, error) {
			return scanConnectPredefinedAttributes(ctx, client, instances, acct, region, st, scanID)
		}},
	)
}

func scanConnectUserList(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListUsers", instances,
		func(instID string) connectPager[*connect.ListUsersOutput] {
			return connect.NewListUsersPaginator(client, &connect.ListUsersInput{InstanceId: &instID},
				func(o *connect.ListUsersPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListUsersOutput) []cttypes.UserSummary { return o.UserSummaryList },
		func(s cttypes.UserSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.UserSummary]{
		rtype: TypeConnectUser, op: "connect:DescribeUser", label: "connect users",
		describe: func(ctx context.Context, it connectListed[cttypes.UserSummary], r *store.Resource) error {
			out, err := client.DescribeUser(ctx, &connect.DescribeUserInput{InstanceId: &it.instanceID, UserId: &it.id})
			if err != nil {
				return err
			}
			u := out.User
			if u == nil {
				return errConnectNoBody
			}
			username := sv(u.Username)
			r.Name = &username
			r.TagsJSON = mapTagsJSON(u.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.UserSummary) {
			username := sv(s.Username)
			r.Name = &username
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectUserHierarchyGroups(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListUserHierarchyGroups", instances,
		func(instID string) connectPager[*connect.ListUserHierarchyGroupsOutput] {
			return connect.NewListUserHierarchyGroupsPaginator(client, &connect.ListUserHierarchyGroupsInput{InstanceId: &instID},
				func(o *connect.ListUserHierarchyGroupsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListUserHierarchyGroupsOutput) []cttypes.HierarchyGroupSummary {
			return o.UserHierarchyGroupSummaryList
		},
		func(s cttypes.HierarchyGroupSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.HierarchyGroupSummary]{
		rtype: TypeConnectUserHierarchyGroup, op: "connect:DescribeUserHierarchyGroup", label: "connect user hierarchy groups",
		describe: func(ctx context.Context, it connectListed[cttypes.HierarchyGroupSummary], r *store.Resource) error {
			out, err := client.DescribeUserHierarchyGroup(ctx, &connect.DescribeUserHierarchyGroupInput{InstanceId: &it.instanceID, HierarchyGroupId: &it.id})
			if err != nil {
				return err
			}
			g := out.HierarchyGroup
			if g == nil {
				return errConnectNoBody
			}
			name := sv(g.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(g.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.HierarchyGroupSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectUserHierarchyStructures emits one row per instance: a
// singleton describing the agent-hierarchy config. NativeID synthesized
// as arn:aws:connect:{region}:{acct}:instance/{instID}/agent-hierarchy.
func scanConnectUserHierarchyStructures(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		out, derr := client.DescribeUserHierarchyStructure(ctx, &connect.DescribeUserHierarchyStructureInput{InstanceId: &instID})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return 0, 0, fmt.Errorf("connect:DescribeUserHierarchyStructure %s: %w", instID, derr)
		}
		if out.HierarchyStructure == nil {
			continue
		}
		arn := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/agent-hierarchy", region, acct.ID, instID)
		name := "agent-hierarchy"
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectUserHierarchyStructure,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert connect user hierarchy structures: %w", uerr)
	}
	return len(batch), n, nil
}

func scanConnectSecurityProfiles(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListSecurityProfiles", instances,
		func(instID string) connectPager[*connect.ListSecurityProfilesOutput] {
			return connect.NewListSecurityProfilesPaginator(client, &connect.ListSecurityProfilesInput{InstanceId: &instID},
				func(o *connect.ListSecurityProfilesPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListSecurityProfilesOutput) []cttypes.SecurityProfileSummary {
			return o.SecurityProfileSummaryList
		},
		func(s cttypes.SecurityProfileSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.SecurityProfileSummary]{
		rtype: TypeConnectSecurityProfile, op: "connect:DescribeSecurityProfile", label: "connect security profiles",
		describe: func(ctx context.Context, it connectListed[cttypes.SecurityProfileSummary], r *store.Resource) error {
			out, err := client.DescribeSecurityProfile(ctx, &connect.DescribeSecurityProfileInput{InstanceId: &it.instanceID, SecurityProfileId: &it.id})
			if err != nil {
				return err
			}
			p := out.SecurityProfile
			if p == nil {
				return errConnectNoBody
			}
			name := sv(p.SecurityProfileName)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(p.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.SecurityProfileSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectPredefinedAttributes — PredefinedAttribute lacks an SDK ARN
// field; synthesize as
// arn:aws:connect:{region}:{acct}:instance/{instID}/predefined-attribute/{name}.
func scanConnectPredefinedAttributes(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents := make([]connectListed[cttypes.InstanceSummary], 0, len(instances))
	for _, inst := range instances {
		if id := sv(inst.Id); id != "" {
			parents = append(parents, connectListed[cttypes.InstanceSummary]{instanceID: id, id: id, arn: sv(inst.Arn), summary: inst})
		}
	}
	listed := connectListUnder(ctx, st, acct, region, "connect:ListPredefinedAttributes", parents,
		func(p connectListed[cttypes.InstanceSummary]) connectPager[*connect.ListPredefinedAttributesOutput] {
			return connect.NewListPredefinedAttributesPaginator(client, &connect.ListPredefinedAttributesInput{InstanceId: &p.id},
				func(o *connect.ListPredefinedAttributesPaginatorOptions) { o.Limit = 100 })
		},
		func(o *connect.ListPredefinedAttributesOutput) []cttypes.PredefinedAttributeSummary {
			return o.PredefinedAttributeSummaryList
		},
		func(instID string, a cttypes.PredefinedAttributeSummary) (string, string) {
			name := sv(a.Name)
			if name == "" {
				return "", ""
			}
			return name, fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/predefined-attribute/%s", region, acct.ID, instID, name)
		})
	type attrSummary = connectChild[cttypes.PredefinedAttributeSummary]
	return connectDescribe[attrSummary]{
		rtype: TypeConnectPredefinedAttribute, op: "connect:DescribePredefinedAttribute", label: "connect predefined attributes",
		describe: func(ctx context.Context, it connectListed[attrSummary], r *store.Resource) error {
			out, err := client.DescribePredefinedAttribute(ctx, &connect.DescribePredefinedAttributeInput{InstanceId: &it.instanceID, Name: &it.id})
			if err != nil {
				return err
			}
			if out.PredefinedAttribute == nil {
				return errConnectNoBody
			}
			r.Name = &it.id
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s attrSummary) {
			name := sv(s.summary.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s.summary)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}
