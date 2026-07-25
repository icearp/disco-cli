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
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanConnectUserList(ctx, client, instances, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanConnectUserHierarchyGroups(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectUserHierarchyStructures(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectSecurityProfiles(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectPredefinedAttributes(ctx, client, instances, acct, region, st, scanID)
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

func scanConnectUserList(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListUsersPaginator(client, &connect.ListUsersInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListUsers", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListUsers %s: %w", instID, perr)
			}
			for _, u := range out.UserSummaryList {
				if u.Id != nil {
					items = append(items, connectInstanceItem{instID, *u.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeUser(gctx, &connect.DescribeUserInput{InstanceId: &k.instanceID, UserId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeUser %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.User == nil {
			return nil, nil
		}
		arn := sv(out.User.Arn)
		if arn == "" {
			return nil, nil
		}
		username := sv(out.User.Username)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectUser,
			NativeID:       arn,
			Name:           &username,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect users")
}

func scanConnectUserHierarchyGroups(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListUserHierarchyGroupsPaginator(client, &connect.ListUserHierarchyGroupsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListUserHierarchyGroups", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListUserHierarchyGroups %s: %w", instID, perr)
			}
			for _, g := range out.UserHierarchyGroupSummaryList {
				if g.Id != nil {
					items = append(items, connectInstanceItem{instID, *g.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeUserHierarchyGroup(gctx, &connect.DescribeUserHierarchyGroupInput{InstanceId: &k.instanceID, HierarchyGroupId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeUserHierarchyGroup %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.HierarchyGroup == nil {
			return nil, nil
		}
		arn := sv(out.HierarchyGroup.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.HierarchyGroup.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectUserHierarchyGroup,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect user hierarchy groups")
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
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListSecurityProfilesPaginator(client, &connect.ListSecurityProfilesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListSecurityProfiles", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListSecurityProfiles %s: %w", instID, perr)
			}
			for _, p := range out.SecurityProfileSummaryList {
				if p.Id != nil {
					items = append(items, connectInstanceItem{instID, *p.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeSecurityProfile(gctx, &connect.DescribeSecurityProfileInput{InstanceId: &k.instanceID, SecurityProfileId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeSecurityProfile %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.SecurityProfile == nil {
			return nil, nil
		}
		arn := sv(out.SecurityProfile.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.SecurityProfile.SecurityProfileName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectSecurityProfile,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect security profiles")
}

// scanConnectPredefinedAttributes — PredefinedAttribute lacks an SDK ARN
// field; synthesize as
// arn:aws:connect:{region}:{acct}:instance/{instID}/predefined-attribute/{name}.
func scanConnectPredefinedAttributes(ctx context.Context, client connectUsersAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListPredefinedAttributesPaginator(client, &connect.ListPredefinedAttributesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListPredefinedAttributes", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListPredefinedAttributes %s: %w", instID, perr)
			}
			for _, a := range out.PredefinedAttributeSummaryList {
				if a.Name != nil {
					items = append(items, connectInstanceItem{instID, *a.Name})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribePredefinedAttribute(gctx, &connect.DescribePredefinedAttributeInput{InstanceId: &k.instanceID, Name: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribePredefinedAttribute %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.PredefinedAttribute == nil {
			return nil, nil
		}
		name := sv(out.PredefinedAttribute.Name)
		if name == "" {
			return nil, nil
		}
		arn := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/predefined-attribute/%s", region, acct.ID, k.instanceID, name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectPredefinedAttribute,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect predefined attributes")
}
