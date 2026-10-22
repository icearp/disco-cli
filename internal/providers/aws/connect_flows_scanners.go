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
// Flows and modules are listed once each: the version and alias phases walk
// the same listings, so a listing error is returned once, by the flow or
// module phase.
func scanConnectFlows(ctx context.Context, client connectFlowsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	// Default ListContactFlows (no ContactFlowTypes filter) returns every flow type.
	flows := connectListPerInstance(ctx, st, acct, region, "connect:ListContactFlows", instances,
		func(instID string) connectPager[*connect.ListContactFlowsOutput] {
			return connect.NewListContactFlowsPaginator(client, &connect.ListContactFlowsInput{InstanceId: &instID},
				func(o *connect.ListContactFlowsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListContactFlowsOutput) []cttypes.ContactFlowSummary { return o.ContactFlowSummaryList },
		func(s cttypes.ContactFlowSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	modules := connectListPerInstance(ctx, st, acct, region, "connect:ListContactFlowModules", instances,
		func(instID string) connectPager[*connect.ListContactFlowModulesOutput] {
			return connect.NewListContactFlowModulesPaginator(client, &connect.ListContactFlowModulesInput{InstanceId: &instID},
				func(o *connect.ListContactFlowModulesPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListContactFlowModulesOutput) []cttypes.ContactFlowModuleSummary {
			return o.ContactFlowModulesSummaryList
		},
		func(s cttypes.ContactFlowModuleSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectRunPhases(st, acct, region,
		connectPhase{"connect contact flows", func() (int, int, error) {
			return scanConnectContactFlows(ctx, client, flows, acct, region, st, scanID)
		}},
		connectPhase{"connect contact flow versions", func() (int, int, error) {
			return scanConnectContactFlowVersions(ctx, client, flows.items, acct, region, st, scanID)
		}},
		connectPhase{"connect contact flow modules", func() (int, int, error) {
			return scanConnectContactFlowModules(ctx, client, modules, acct, region, st, scanID)
		}},
		connectPhase{"connect contact flow module versions", func() (int, int, error) {
			return scanConnectContactFlowModuleVersions(ctx, client, modules.items, acct, region, st, scanID)
		}},
		connectPhase{"connect contact flow module aliases", func() (int, int, error) {
			return scanConnectContactFlowModuleAliases(ctx, client, modules.items, acct, region, st, scanID)
		}},
	)
}

// connectVersionName is a version row's name: {parentID}:{version}.
func connectVersionName(parentID string, version *int64) string {
	ver := int64(0)
	if version != nil {
		ver = *version
	}
	return fmt.Sprintf("%s:%d", parentID, ver)
}

func scanConnectContactFlows(ctx context.Context, client connectFlowsAPI, listed *connectListing[cttypes.ContactFlowSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return connectDescribe[cttypes.ContactFlowSummary]{
		rtype: TypeConnectContactFlow, op: "connect:DescribeContactFlow", label: "connect contact flows",
		notReady: "ContactFlowNotPublishedException", why: "a flow never published has no published content to describe",
		describe: func(ctx context.Context, it connectListed[cttypes.ContactFlowSummary], r *store.Resource) error {
			out, err := client.DescribeContactFlow(ctx, &connect.DescribeContactFlowInput{InstanceId: &it.instanceID, ContactFlowId: &it.id})
			if err != nil {
				return err
			}
			f := out.ContactFlow
			if f == nil {
				return errConnectNoBody
			}
			name, status := sv(f.Name), string(f.Status)
			r.Name, r.Status = &name, &status
			r.TagsJSON = mapTagsJSON(f.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.ContactFlowSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectContactFlowModules(ctx context.Context, client connectFlowsAPI, listed *connectListing[cttypes.ContactFlowModuleSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return connectDescribe[cttypes.ContactFlowModuleSummary]{
		rtype: TypeConnectContactFlowModule, op: "connect:DescribeContactFlowModule", label: "connect contact flow modules",
		describe: func(ctx context.Context, it connectListed[cttypes.ContactFlowModuleSummary], r *store.Resource) error {
			out, err := client.DescribeContactFlowModule(ctx, &connect.DescribeContactFlowModuleInput{InstanceId: &it.instanceID, ContactFlowModuleId: &it.id})
			if err != nil {
				return err
			}
			m := out.ContactFlowModule
			if m == nil {
				return errConnectNoBody
			}
			name, status := sv(m.Name), string(m.Status)
			r.Name, r.Status = &name, &status
			r.TagsJSON = mapTagsJSON(m.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.ContactFlowModuleSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectContactFlowVersions enumerates per-flow versions. SDK has no
// DescribeContactFlowVersion — store list summary as attrs.
func scanConnectContactFlowVersions(ctx context.Context, client connectFlowsAPI, flows []connectListed[cttypes.ContactFlowSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListUnder(ctx, st, acct, region, "connect:ListContactFlowVersions", flows,
		func(f connectListed[cttypes.ContactFlowSummary]) connectPager[*connect.ListContactFlowVersionsOutput] {
			return connect.NewListContactFlowVersionsPaginator(client, &connect.ListContactFlowVersionsInput{InstanceId: &f.instanceID, ContactFlowId: &f.id},
				func(o *connect.ListContactFlowVersionsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListContactFlowVersionsOutput) []cttypes.ContactFlowVersionSummary {
			return o.ContactFlowVersionSummaryList
		},
		func(flowID string, v cttypes.ContactFlowVersionSummary) (string, string) {
			return connectVersionName(flowID, v.Version), sv(v.Arn)
		})
	return connectStoreListed(st, acct, region, scanID, TypeConnectContactFlowVersion, "connect contact flow versions", listed,
		func(r *store.Resource, it connectListed[connectChild[cttypes.ContactFlowVersionSummary]]) {
			r.Name = &it.id
			r.AttributesJSON = mustJSON(it.summary.summary)
		})
}

// scanConnectContactFlowModuleVersions enumerates per-module versions, list
// summary as attrs.
func scanConnectContactFlowModuleVersions(ctx context.Context, client connectFlowsAPI, modules []connectListed[cttypes.ContactFlowModuleSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListUnder(ctx, st, acct, region, "connect:ListContactFlowModuleVersions", modules,
		func(m connectListed[cttypes.ContactFlowModuleSummary]) connectPager[*connect.ListContactFlowModuleVersionsOutput] {
			return connect.NewListContactFlowModuleVersionsPaginator(client, &connect.ListContactFlowModuleVersionsInput{InstanceId: &m.instanceID, ContactFlowModuleId: &m.id},
				func(o *connect.ListContactFlowModuleVersionsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListContactFlowModuleVersionsOutput) []cttypes.ContactFlowModuleVersionSummary {
			return o.ContactFlowModuleVersionSummaryList
		},
		func(moduleID string, v cttypes.ContactFlowModuleVersionSummary) (string, string) {
			return connectVersionName(moduleID, v.Version), sv(v.Arn)
		})
	return connectStoreListed(st, acct, region, scanID, TypeConnectContactFlowModuleVersion, "connect contact flow module versions", listed,
		func(r *store.Resource, it connectListed[connectChild[cttypes.ContactFlowModuleVersionSummary]]) {
			r.Name = &it.id
			r.AttributesJSON = mustJSON(it.summary.summary)
		})
}

// scanConnectContactFlowModuleAliases lists each module's aliases and stores
// each as its DescribeContactFlowModuleAlias body.
func scanConnectContactFlowModuleAliases(ctx context.Context, client connectFlowsAPI, modules []connectListed[cttypes.ContactFlowModuleSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListUnder(ctx, st, acct, region, "connect:ListContactFlowModuleAliases", modules,
		func(m connectListed[cttypes.ContactFlowModuleSummary]) connectPager[*connect.ListContactFlowModuleAliasesOutput] {
			return connect.NewListContactFlowModuleAliasesPaginator(client, &connect.ListContactFlowModuleAliasesInput{InstanceId: &m.instanceID, ContactFlowModuleId: &m.id},
				func(o *connect.ListContactFlowModuleAliasesPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListContactFlowModuleAliasesOutput) []cttypes.ContactFlowModuleAliasSummary {
			return o.ContactFlowModuleAliasSummaryList
		},
		func(_ string, a cttypes.ContactFlowModuleAliasSummary) (string, string) {
			return sv(a.AliasId), sv(a.Arn)
		})
	type aliasSummary = connectChild[cttypes.ContactFlowModuleAliasSummary]
	return connectDescribe[aliasSummary]{
		rtype: TypeConnectContactFlowModuleAlias, op: "connect:DescribeContactFlowModuleAlias", label: "connect contact flow module aliases",
		describe: func(ctx context.Context, it connectListed[aliasSummary], r *store.Resource) error {
			out, err := client.DescribeContactFlowModuleAlias(ctx, &connect.DescribeContactFlowModuleAliasInput{
				InstanceId: &it.instanceID, ContactFlowModuleId: &it.summary.parentID, AliasId: &it.id,
			})
			if err != nil {
				return err
			}
			a := out.ContactFlowModuleAlias
			if a == nil {
				return errConnectNoBody
			}
			name := sv(a.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s aliasSummary) {
			name := sv(s.summary.AliasName)
			r.Name = &name
			r.AttributesJSON = mustJSON(s.summary)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}
