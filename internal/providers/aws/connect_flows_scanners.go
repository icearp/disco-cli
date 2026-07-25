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
	registerType(restype.Descriptor{Type: TypeConnectContactFlow, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectContactFlowVersion, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectContactFlowModule, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectContactFlowModuleVersion, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectContactFlowModuleAlias, Service: "connect"})
}

// connectFlowsAPI is the narrow surface used by the Flows family.
type connectFlowsAPI interface {
	ListContactFlows(context.Context, *connect.ListContactFlowsInput, ...func(*connect.Options)) (*connect.ListContactFlowsOutput, error)
	DescribeContactFlow(context.Context, *connect.DescribeContactFlowInput, ...func(*connect.Options)) (*connect.DescribeContactFlowOutput, error)
	ListContactFlowVersions(context.Context, *connect.ListContactFlowVersionsInput, ...func(*connect.Options)) (*connect.ListContactFlowVersionsOutput, error)
	ListContactFlowModules(context.Context, *connect.ListContactFlowModulesInput, ...func(*connect.Options)) (*connect.ListContactFlowModulesOutput, error)
	DescribeContactFlowModule(context.Context, *connect.DescribeContactFlowModuleInput, ...func(*connect.Options)) (*connect.DescribeContactFlowModuleOutput, error)
	ListContactFlowModuleVersions(context.Context, *connect.ListContactFlowModuleVersionsInput, ...func(*connect.Options)) (*connect.ListContactFlowModuleVersionsOutput, error)
	ListContactFlowModuleAliases(context.Context, *connect.ListContactFlowModuleAliasesInput, ...func(*connect.Options)) (*connect.ListContactFlowModuleAliasesOutput, error)
	DescribeContactFlowModuleAlias(context.Context, *connect.DescribeContactFlowModuleAliasInput, ...func(*connect.Options)) (*connect.DescribeContactFlowModuleAliasOutput, error)
}

// scanConnectFlows runs the Flows-family phases per instance. ContactFlow and
// ContactFlowModule versions are list-only (no Describe op) — stored as list
// summary. ContactFlowModuleAlias has a Describe op and stores the full body.
func scanConnectFlows(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanConnectContactFlows(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectContactFlowVersions(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectContactFlowModules(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectContactFlowModuleVersionsAndAliases(ctx, client, instances, acct, region, st, scanID)
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

// listFlowsByInstance returns (instanceID, flowID) pairs across all instances.
// Default ListContactFlows (no ContactFlowTypes filter) returns every flow type.
func listFlowsByInstance(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store) ([]connectInstanceItem, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListContactFlowsPaginator(client, &connect.ListContactFlowsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListContactFlows", acct.ID, region, perr)
					break
				}
				return nil, fmt.Errorf("connect:ListContactFlows %s: %w", instID, perr)
			}
			for _, f := range out.ContactFlowSummaryList {
				if f.Id != nil {
					items = append(items, connectInstanceItem{instID, *f.Id})
				}
			}
		}
	}
	return items, nil
}

func scanConnectContactFlows(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	items, lerr := listFlowsByInstance(ctx, client, instances, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeContactFlow(gctx, &connect.DescribeContactFlowInput{InstanceId: &k.instanceID, ContactFlowId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeContactFlow %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.ContactFlow == nil {
			return nil, nil
		}
		arn := sv(out.ContactFlow.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.ContactFlow.Name)
		status := string(out.ContactFlow.Status)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectContactFlow,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect contact flows")
}

// scanConnectContactFlowVersions enumerates per-flow versions. SDK has no
// DescribeContactFlowVersion — store list summary as attrs.
func scanConnectContactFlowVersions(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	flows, lerr := listFlowsByInstance(ctx, client, instances, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}
	var batch []*store.Resource
	for _, f := range flows {
		fid := f.id
		instID := f.instanceID
		pager := connect.NewListContactFlowVersionsPaginator(client, &connect.ListContactFlowVersionsInput{InstanceId: &instID, ContactFlowId: &fid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListContactFlowVersions %s/%s: %w", instID, fid, perr)
			}
			for _, v := range out.ContactFlowVersionSummaryList {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				ver := int64(0)
				if v.Version != nil {
					ver = *v.Version
				}
				name := fmt.Sprintf("%s:%d", fid, ver)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectContactFlowVersion,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert connect contact flow versions: %w", uerr)
	}
	return len(batch), n, nil
}

func listFlowModulesByInstance(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store) ([]connectInstanceItem, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		var token *string
		for {
			out, perr := client.ListContactFlowModules(ctx, &connect.ListContactFlowModulesInput{InstanceId: &instID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListContactFlowModules", acct.ID, region, perr)
					break
				}
				return nil, fmt.Errorf("connect:ListContactFlowModules %s: %w", instID, perr)
			}
			for _, m := range out.ContactFlowModulesSummaryList {
				if m.Id != nil {
					items = append(items, connectInstanceItem{instID, *m.Id})
				}
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return items, nil
}

func scanConnectContactFlowModules(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	items, lerr := listFlowModulesByInstance(ctx, client, instances, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeContactFlowModule(gctx, &connect.DescribeContactFlowModuleInput{InstanceId: &k.instanceID, ContactFlowModuleId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeContactFlowModule %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.ContactFlowModule == nil {
			return nil, nil
		}
		arn := sv(out.ContactFlowModule.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.ContactFlowModule.Name)
		status := string(out.ContactFlowModule.Status)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectContactFlowModule,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect contact flow modules")
}

// scanConnectContactFlowModuleVersionsAndAliases combines the two per-module
// sub-resources into one phase to share a single ListContactFlowModules
// pre-pass — avoids walking it twice.
func scanConnectContactFlowModuleVersionsAndAliases(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	modules, lerr := listFlowModulesByInstance(ctx, client, instances, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}
	var batch []*store.Resource
	type aliasKey struct {
		instanceID, moduleID, aliasID, arn string
	}
	var aliasItems []aliasKey
	for _, m := range modules {
		mid := m.id
		instID := m.instanceID

		// Versions: list-only, summary as attrs.
		vPager := connect.NewListContactFlowModuleVersionsPaginator(client, &connect.ListContactFlowModuleVersionsInput{InstanceId: &instID, ContactFlowModuleId: &mid})
		for vPager.HasMorePages() {
			out, perr := vPager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListContactFlowModuleVersions %s/%s: %w", instID, mid, perr)
			}
			for _, v := range out.ContactFlowModuleVersionSummaryList {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				ver := int64(0)
				if v.Version != nil {
					ver = *v.Version
				}
				name := fmt.Sprintf("%s:%d", mid, ver)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectContactFlowModuleVersion,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
		}

		// Aliases: list keys then fan out Describe below.
		var token *string
		for {
			out, perr := client.ListContactFlowModuleAliases(ctx, &connect.ListContactFlowModuleAliasesInput{InstanceId: &instID, ContactFlowModuleId: &mid, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListContactFlowModuleAliases %s/%s: %w", instID, mid, perr)
			}
			for _, a := range out.ContactFlowModuleAliasSummaryList {
				if a.AliasId != nil && a.Arn != nil {
					aliasItems = append(aliasItems, aliasKey{instID, mid, *a.AliasId, *a.Arn})
				}
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}

	// Upsert versions first.
	if len(batch) > 0 {
		if _, uerr := st.UpsertResources(batch); uerr != nil {
			return 0, 0, fmt.Errorf("upsert connect contact flow module versions: %w", uerr)
		}
	}

	// Aliases via Describe fan-out.
	var aliasBatch []*store.Resource
	for _, k := range aliasItems {
		ak := k
		out, derr := client.DescribeContactFlowModuleAlias(ctx, &connect.DescribeContactFlowModuleAliasInput{InstanceId: &ak.instanceID, ContactFlowModuleId: &ak.moduleID, AliasId: &ak.aliasID})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return 0, 0, fmt.Errorf("connect:DescribeContactFlowModuleAlias %s/%s/%s: %w", ak.instanceID, ak.moduleID, ak.aliasID, derr)
		}
		arn := ak.arn
		if arn == "" {
			continue
		}
		var name string
		if out.ContactFlowModuleAlias != nil {
			name = sv(out.ContactFlowModuleAlias.Name)
		}
		aliasBatch = append(aliasBatch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectContactFlowModuleAlias,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		})
	}
	if len(aliasBatch) > 0 {
		if _, uerr := st.UpsertResources(aliasBatch); uerr != nil {
			return 0, 0, fmt.Errorf("upsert connect contact flow module aliases: %w", uerr)
		}
	}
	return len(batch) + len(aliasBatch), len(batch) + len(aliasBatch), nil
}
