package aws

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/drs"
	drstypes "github.com/aws/aws-sdk-go-v2/service/drs/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// AWS Service Reference names every DRS resource type with a "Resource" suffix
// (SourceServerResource, RecoveryInstanceResource, …); DRS has no CloudFormation
// twin, so disco types mirror that spelling exactly — the algorithmic key
// matches with no alias. Recovery-plan steps and launch actions have no Service
// Reference resource and carry no suffix, as does a source server's
// replication configuration. A launch-action parameter is typed
// SSM_STORE or DYNAMIC, and the model restricts its value to ^[A-Za-z0-9.-]+$:
// a reference to a Parameter Store parameter or a launch-time value, too narrow
// to carry a secret, so no Redact rule applies.
func init() {
	registerType(restype.Descriptor{Type: TypeDRSSourceServerResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSRecoveryInstanceResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSSourceNetworkResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSReplicationConfigurationTemplateResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSLaunchConfigurationTemplateResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSRecoveryPlanResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSRecoveryPlanStep, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSLaunchAction, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSReplicationConfiguration, Service: "drs"})
	registerService(serviceEntry{
		name: "aws:drs",
		fn:   scanDRS,
	})
}

type drsAPI interface {
	DescribeSourceServers(context.Context, *drs.DescribeSourceServersInput, ...func(*drs.Options)) (*drs.DescribeSourceServersOutput, error)
	DescribeRecoveryInstances(context.Context, *drs.DescribeRecoveryInstancesInput, ...func(*drs.Options)) (*drs.DescribeRecoveryInstancesOutput, error)
	DescribeSourceNetworks(context.Context, *drs.DescribeSourceNetworksInput, ...func(*drs.Options)) (*drs.DescribeSourceNetworksOutput, error)
	DescribeReplicationConfigurationTemplates(context.Context, *drs.DescribeReplicationConfigurationTemplatesInput, ...func(*drs.Options)) (*drs.DescribeReplicationConfigurationTemplatesOutput, error)
	DescribeLaunchConfigurationTemplates(context.Context, *drs.DescribeLaunchConfigurationTemplatesInput, ...func(*drs.Options)) (*drs.DescribeLaunchConfigurationTemplatesOutput, error)
	ListRecoveryPlans(context.Context, *drs.ListRecoveryPlansInput, ...func(*drs.Options)) (*drs.ListRecoveryPlansOutput, error)
	GetRecoveryPlan(context.Context, *drs.GetRecoveryPlanInput, ...func(*drs.Options)) (*drs.GetRecoveryPlanOutput, error)
	ListRecoveryPlanSteps(context.Context, *drs.ListRecoveryPlanStepsInput, ...func(*drs.Options)) (*drs.ListRecoveryPlanStepsOutput, error)
	ListLaunchActions(context.Context, *drs.ListLaunchActionsInput, ...func(*drs.Options)) (*drs.ListLaunchActionsOutput, error)
	GetReplicationConfiguration(context.Context, *drs.GetReplicationConfigurationInput, ...func(*drs.Options)) (*drs.GetReplicationConfigurationOutput, error)
}

// scanDRS discovers DRS source servers with their replication configurations,
// recovery instances, source networks, replication / launch configuration
// templates, the launch actions of templates and source servers, and recovery
// plans with their steps. Jobs and recovery-plan executions (ephemeral run
// records) are not scanned.
//
// Phases run in order and stop at the first hard error. The launch actions run
// after every pre-existing phase, and the recovery-plan phases after them: the
// plan API is the newest surface, and its failure must not cost the launch
// actions, which need only the servers and templates. The per-server
// replication configurations run last, so their failure costs no earlier phase.
func scanDRS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := drs.NewFromConfig(acct.cfg, func(o *drs.Options) { o.Region = region })
	var servers, templates, plans []childParent
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			t, i, servers, err = scanDRSSourceServers(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanDRSRecoveryInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDRSSourceNetworks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDRSReplicationTemplates(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			t, i, templates, err = scanDRSLaunchTemplates(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "drs:ListLaunchActions", slices.Concat(templates, servers), isDRSNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listDRSLaunchActions(ctx, client, acct, region, scanID, p)
				})
		},
		func() (t, i int, err error) {
			t, i, plans, err = scanDRSRecoveryPlans(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "drs:ListRecoveryPlanSteps", plans, isDRSNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listDRSRecoveryPlanSteps(ctx, client, acct, region, scanID, p)
				})
		},
		func() (int, int, error) {
			return scanDRSReplicationConfigurations(ctx, client, acct, region, st, scanID, servers)
		},
	} {
		t, i, perr := phase()
		if perr != nil {
			if isAccountNotInitialized(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanDRSSourceServers also returns the stored servers for the launch-action
// phase.
func scanDRSSourceServers(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	pager := drs.NewDescribeSourceServersPaginator(client, &drs.DescribeSourceServersInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "drs:DescribeSourceServers", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("drs:DescribeSourceServers: %w", err)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			if id := sv(s.SourceServerID); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSSourceServerResource, NativeID: arn,
				Name: s.SourceServerID, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "drs source-servers")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

func scanDRSRecoveryInstances(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeRecoveryInstancesPaginator(client, &drs.DescribeRecoveryInstancesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeRecoveryInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeRecoveryInstances: %w", err)
		}
		for _, ri := range out.Items {
			arn := sv(ri.Arn)
			if arn == "" {
				continue
			}
			status := string(ri.Ec2InstanceState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSRecoveryInstanceResource, NativeID: arn,
				Name: ri.RecoveryInstanceID, Region: &region, Status: &status,
				AttributesJSON: mustJSON(ri), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs recovery-instances")
}

func scanDRSSourceNetworks(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeSourceNetworksPaginator(client, &drs.DescribeSourceNetworksInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeSourceNetworks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeSourceNetworks: %w", err)
		}
		for _, sn := range out.Items {
			arn := sv(sn.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSSourceNetworkResource, NativeID: arn,
				Name: sn.SourceNetworkID, Region: &region, AttributesJSON: mustJSON(sn), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs source-networks")
}

func scanDRSReplicationTemplates(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeReplicationConfigurationTemplatesPaginator(client, &drs.DescribeReplicationConfigurationTemplatesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeReplicationConfigurationTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeReplicationConfigurationTemplates: %w", err)
		}
		for _, rt := range out.Items {
			arn := sv(rt.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSReplicationConfigurationTemplateResource, NativeID: arn,
				Name: rt.ReplicationConfigurationTemplateID, Region: &region,
				AttributesJSON: mustJSON(rt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs replication-configuration-templates")
}

// scanDRSLaunchTemplates also returns the stored templates for the
// launch-action phase.
func scanDRSLaunchTemplates(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	pager := drs.NewDescribeLaunchConfigurationTemplatesPaginator(client, &drs.DescribeLaunchConfigurationTemplatesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "drs:DescribeLaunchConfigurationTemplates", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("drs:DescribeLaunchConfigurationTemplates: %w", err)
		}
		for _, lt := range out.Items {
			arn := sv(lt.Arn)
			if arn == "" {
				continue
			}
			if id := sv(lt.LaunchConfigurationTemplateID); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSLaunchConfigurationTemplateResource, NativeID: arn,
				Name: lt.LaunchConfigurationTemplateID, Region: &region,
				AttributesJSON: mustJSON(lt), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "drs launch-configuration-templates")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

// scanDRSReplicationConfigurations reads the replication configuration of each
// source server the server phase stored. The response carries no ARN or id of
// its own, only the server's id, so the NativeID is
// {serverARN}/replication-configuration. A server whose call still fails
// transiently after the SDK's retries is skipped with one warning, keeping its
// siblings' rows.
func scanDRSReplicationConfigurations(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string, servers []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, "drs:GetReplicationConfiguration", servers, isDRSNotFound, byParentID,
		func(ctx context.Context, server childParent) ([]*store.Resource, error) {
			out, err := client.GetReplicationConfiguration(ctx, &drs.GetReplicationConfigurationInput{SourceServerID: &server.id})
			if err != nil {
				return nil, err
			}
			return []*store.Resource{{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSReplicationConfiguration, NativeID: server.arn + "/replication-configuration",
				Name: out.Name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}}, nil
		})
}

// isDRSNotFound matches a recovery plan, launch configuration template or
// source server deleted between its list call and the call that reads it.
func isDRSNotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanDRSRecoveryPlans lists plan ARNs, then reads each plan with
// GetRecoveryPlan: only the full plan carries Description and Tags. A denial
// mid-listing warns and keeps the plans listed so far. It also returns the
// stored plans for the step phase.
func scanDRSRecoveryPlans(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var arns []string
	pager := drs.NewListRecoveryPlansPaginator(client, &drs.ListRecoveryPlansInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "drs:ListRecoveryPlans", acct.ID, region, err)
				break
			}
			return 0, 0, nil, fmt.Errorf("drs:ListRecoveryPlans: %w", err)
		}
		for _, p := range out.RecoveryPlans {
			if arn := sv(p.RecoveryPlanArn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	rows, err := describeFanOut(ctx, st, acct, region, "drs:GetRecoveryPlan", arns, isDRSNotFound,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.GetRecoveryPlan(ctx, &drs.GetRecoveryPlanInput{RecoveryPlanArn: &arn})
			if err != nil {
				return nil, err
			}
			p := out.RecoveryPlan
			if p == nil {
				return nil, nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSRecoveryPlanResource, NativeID: arn, Name: p.Name, Region: &region,
				Status: nonEmptyPtr(string(p.Status)), AttributesJSON: mustJSON(p), TagsJSON: mapTagsJSON(p.Tags),
				DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, nil, err
	}
	parents := make([]childParent, len(rows))
	for i, r := range rows {
		parents[i] = childParent{id: r.NativeID, arn: r.NativeID}
	}
	total, inserted, err := upsertBatch(st, rows, "drs recovery-plans")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

// drsRecoveryPlanStepAttrs shadows the step's Configuration union, which
// marshals as an unlabelled {"Value": ...}, with one key per known member so the
// stored row says whether the step launches servers or waits. A member newer
// than the pinned SDK is kept under UnknownMember with its tag and raw value.
type drsRecoveryPlanStepAttrs struct {
	drstypes.RecoveryPlanStep
	Configuration drsRecoveryPlanStepConfig
}

type drsRecoveryPlanStepConfig struct {
	ServerStepConfiguration *drstypes.ServerStepConfiguration `json:",omitempty"`
	WaitStepConfiguration   *drstypes.WaitStepConfiguration   `json:",omitempty"`
	UnknownMember           *drstypes.UnknownUnionMember      `json:",omitempty"`
}

func flattenDRSRecoveryPlanStep(step drstypes.RecoveryPlanStep) drsRecoveryPlanStepAttrs {
	out := drsRecoveryPlanStepAttrs{RecoveryPlanStep: step}
	switch c := step.Configuration.(type) {
	case *drstypes.RecoveryPlanStepConfigurationMemberServerStepConfiguration:
		out.Configuration.ServerStepConfiguration = &c.Value
	case *drstypes.RecoveryPlanStepConfigurationMemberWaitStepConfiguration:
		out.Configuration.WaitStepConfiguration = &c.Value
	case *drstypes.UnknownUnionMember:
		out.Configuration.UnknownMember = c
	}
	return out
}

func listDRSRecoveryPlanSteps(ctx context.Context, client drsAPI, acct *account, region, scanID string, plan childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := drs.NewListRecoveryPlanStepsPaginator(client, &drs.ListRecoveryPlanStepsInput{RecoveryPlanArn: &plan.id})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, step := range out.RecoveryPlanSteps {
			arn := sv(step.RecoveryPlanStepArn)
			if arn == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSRecoveryPlanStep, NativeID: arn, Name: step.StepName, Region: &region,
				AttributesJSON: mustJSON(flattenDRSRecoveryPlanStep(step)), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

// listDRSLaunchActions lists the launch actions of one launch configuration
// template or source server; ListLaunchActions takes either id. Actions carry
// no ARN and the Service Reference defines no action resource, so the NativeID
// is {parentARN}/launch-action/{actionId}: the same action id under two
// parents stays two rows.
func listDRSLaunchActions(ctx context.Context, client drsAPI, acct *account, region, scanID string, parent childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := drs.NewListLaunchActionsPaginator(client, &drs.ListLaunchActionsInput{ResourceId: &parent.id})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range out.Items {
			id := sv(a.ActionId)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSLaunchAction, NativeID: parent.arn + "/launch-action/" + id, Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
