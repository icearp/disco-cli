package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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

// The ops of scanDRS's phases, shared by each phase and the scanDRS entry
// that warns under it if its error is demoted.
const (
	drsSourceServersOp        = "drs:DescribeSourceServers"
	drsRecoveryInstancesOp    = "drs:DescribeRecoveryInstances"
	drsSourceNetworksOp       = "drs:DescribeSourceNetworks"
	drsReplicationTemplatesOp = "drs:DescribeReplicationConfigurationTemplates"
	drsLaunchTemplatesOp      = "drs:DescribeLaunchConfigurationTemplates"
	drsLaunchActionsOp        = "drs:ListLaunchActions"
	drsRecoveryPlansOp        = "drs:ListRecoveryPlans"
	drsGetRecoveryPlanOp      = "drs:GetRecoveryPlan"
	drsRecoveryPlanStepsOp    = "drs:ListRecoveryPlanSteps"
	drsReplicationConfigOp    = "drs:GetReplicationConfiguration"
)

// drsPageMax is the MaxResults cap the Smithy model sets on
// DescribeLaunchConfigurationTemplates, ListRecoveryPlans,
// ListRecoveryPlanSteps and ListLaunchActions. The other Describe ops model no
// cap and are called without MaxResults.
const drsPageMax = 1000

// drsPhase is one phase of scanDRS and the op it is warned under.
type drsPhase struct {
	op  string
	run func() (int, int, error)
}

// scanDRS discovers DRS source servers with their replication configurations,
// recovery instances, source networks, replication / launch configuration
// templates, the launch actions of templates and source servers, and recovery
// plans with their steps. Jobs and recovery-plan executions (ephemeral run
// records) are not scanned.
//
// Phases run in order. A phase's error does not stop the later phases; only a
// failed store write does, since every later write would fail too. A child
// phase reads the parents its parent phase stored, so a failed parent phase
// leaves it fewer parents.
func scanDRS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := drs.NewFromConfig(acct.cfg, func(o *drs.Options) { o.Region = region })
	var servers, templates, plans []childParent
	phases := []drsPhase{
		{drsSourceServersOp, func() (t, i int, err error) {
			t, i, servers, err = scanDRSSourceServers(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{drsRecoveryInstancesOp, func() (int, int, error) {
			return scanDRSRecoveryInstances(ctx, client, acct, region, st, scanID)
		}},
		{drsSourceNetworksOp, func() (int, int, error) { return scanDRSSourceNetworks(ctx, client, acct, region, st, scanID) }},
		{drsReplicationTemplatesOp, func() (int, int, error) {
			return scanDRSReplicationTemplates(ctx, client, acct, region, st, scanID)
		}},
		{drsLaunchTemplatesOp, func() (t, i int, err error) {
			t, i, templates, err = scanDRSLaunchTemplates(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{drsLaunchActionsOp, func() (int, int, error) {
			return scanDRSLaunchActions(ctx, client, acct, region, st, scanID, slices.Concat(templates, servers))
		}},
		{drsRecoveryPlansOp, func() (t, i int, err error) {
			t, i, plans, err = scanDRSRecoveryPlans(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{drsRecoveryPlanStepsOp, func() (int, int, error) {
			return scanDRSRecoveryPlanSteps(ctx, client, acct, region, st, scanID, plans)
		}},
		{drsReplicationConfigOp, func() (int, int, error) {
			return scanDRSReplicationConfigurations(ctx, client, acct, region, st, scanID, servers)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		// Every DRS op models UninitializedAccountException, which an account
		// never onboarded to DRS returns. It reads as the service disabled
		// only while no phase has stored a row or failed otherwise, so no
		// count or error is discarded.
		if isAccountNotInitialized(err) && p.total+t == 0 && len(p.errs) == 0 {
			return 0, 0, markServiceDisabled(err)
		}
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// drsRow builds the row of a DRS resource listed with its ARN and id; the id
// is the row's name. It returns why the resource cannot be stored when the ARN
// is empty.
func drsRow(acct *account, region, scanID, rtype, what string, arn, id *string, attrs any, tags map[string]string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty ARN", sv(id)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: id, Region: &region,
		AttributesJSON: mustJSON(attrs), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

// scanDRSSourceServers also returns the stored servers for the launch-action
// and replication-configuration phases.
func scanDRSSourceServers(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentList(ctx, st, acct, region, drsSourceServersOp, "source servers", "SourceServerID",
		func(ctx context.Context, token *string) ([]drstypes.SourceServer, *string, error) {
			out, err := client.DescribeSourceServers(ctx, &drs.DescribeSourceServersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(s drstypes.SourceServer) (*store.Resource, string, string) {
			return drsRow(acct, region, scanID, TypeDRSSourceServerResource, "source servers", s.Arn, s.SourceServerID, s, s.Tags)
		},
		func(s drstypes.SourceServer) string { return sv(s.SourceServerID) })
}

func scanDRSRecoveryInstances(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, drsRecoveryInstancesOp,
		func(ctx context.Context, token *string) ([]drstypes.RecoveryInstance, *string, error) {
			out, err := client.DescribeRecoveryInstances(ctx, &drs.DescribeRecoveryInstancesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(ri drstypes.RecoveryInstance) (*store.Resource, string, string) {
			r, why, key := drsRow(acct, region, scanID, TypeDRSRecoveryInstanceResource, "recovery instances", ri.Arn, ri.RecoveryInstanceID, ri, ri.Tags)
			if r != nil {
				r.Status = nonEmptyPtr(string(ri.Ec2InstanceState))
			}
			return r, why, key
		})
}

func scanDRSSourceNetworks(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, drsSourceNetworksOp,
		func(ctx context.Context, token *string) ([]drstypes.SourceNetwork, *string, error) {
			out, err := client.DescribeSourceNetworks(ctx, &drs.DescribeSourceNetworksInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(sn drstypes.SourceNetwork) (*store.Resource, string, string) {
			return drsRow(acct, region, scanID, TypeDRSSourceNetworkResource, "source networks", sn.Arn, sn.SourceNetworkID, sn, sn.Tags)
		})
}

// scanDRSReplicationTemplates stores each template's own Tags; its
// StagingAreaTags, the tags DRS puts on the staging resources it creates,
// stay in the attributes.
func scanDRSReplicationTemplates(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, drsReplicationTemplatesOp,
		func(ctx context.Context, token *string) ([]drstypes.ReplicationConfigurationTemplate, *string, error) {
			out, err := client.DescribeReplicationConfigurationTemplates(ctx, &drs.DescribeReplicationConfigurationTemplatesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(rt drstypes.ReplicationConfigurationTemplate) (*store.Resource, string, string) {
			return drsRow(acct, region, scanID, TypeDRSReplicationConfigurationTemplateResource, "replication configuration templates",
				rt.Arn, rt.ReplicationConfigurationTemplateID, rt, rt.Tags)
		})
}

// scanDRSLaunchTemplates also returns the stored templates for the
// launch-action phase.
func scanDRSLaunchTemplates(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentList(ctx, st, acct, region, drsLaunchTemplatesOp, "launch configuration templates", "LaunchConfigurationTemplateID",
		func(ctx context.Context, token *string) ([]drstypes.LaunchConfigurationTemplate, *string, error) {
			out, err := client.DescribeLaunchConfigurationTemplates(ctx, &drs.DescribeLaunchConfigurationTemplatesInput{
				NextToken: token, MaxResults: sdkaws.Int32(drsPageMax),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(lt drstypes.LaunchConfigurationTemplate) (*store.Resource, string, string) {
			return drsRow(acct, region, scanID, TypeDRSLaunchConfigurationTemplateResource, "launch configuration templates",
				lt.Arn, lt.LaunchConfigurationTemplateID, lt, lt.Tags)
		},
		func(lt drstypes.LaunchConfigurationTemplate) string { return sv(lt.LaunchConfigurationTemplateID) })
}

// storeDRSChildren reads op's items for each parent through storeTokenEachParent
// (concurrently), then links every stored child under the parent it was
// read from. A parent ResourceNotFoundException (modeled by every child op
// this file calls) reports was deleted since it was listed.
func storeDRSChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	page func(ctx context.Context, parentID string, token *string) ([]Item, *string, error),
	row func(parent childParent, it Item) (*store.Resource, string, string),
) (int, int, error) {
	byID := make(map[string]childParent, len(parents))
	ids := make([]string, len(parents))
	for i, p := range parents {
		byID[p.id], ids[i] = p, p.id
	}
	var pairs [][2]string
	total, inserted, err := storeTokenEachParent(ctx, st, acct, region, op, []string{"ResourceNotFoundException"}, ids, page,
		func(id string, it Item) (*store.Resource, string, string) {
			p := byID[id]
			r, why, key := row(p, it)
			if r != nil {
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, r.NativeID), store.ResourceID("aws", acct.ID, p.arn)})
			}
			return r, why, key
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if herr := st.RecordHierarchyBatch(pairs); herr != nil {
		return total, inserted, errors.Join(err, fmt.Errorf("closure %s: %w", op, herr))
	}
	return total, inserted, err
}

// scanDRSLaunchActions lists the launch actions of each launch configuration
// template and source server; ListLaunchActions takes either id. Actions
// carry no ARN and the Service Reference defines no action resource, so the
// NativeID is {parentARN}/launch-action/{actionId}: the same action id under
// two parents stays two rows.
func scanDRSLaunchActions(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string, parents []childParent) (int, int, error) {
	return storeDRSChildren(ctx, st, acct, region, drsLaunchActionsOp, parents,
		func(ctx context.Context, id string, token *string) ([]drstypes.LaunchAction, *string, error) {
			out, err := client.ListLaunchActions(ctx, &drs.ListLaunchActionsInput{ResourceId: &id, NextToken: token, MaxResults: sdkaws.Int32(drsPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(parent childParent, a drstypes.LaunchAction) (*store.Resource, string, string) {
			id := sv(a.ActionId)
			if id == "" {
				return nil, "launch actions have an empty ActionId", parent.arn + " " + sv(a.Name)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSLaunchAction, NativeID: parent.arn + "/launch-action/" + id, Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanDRSRecoveryPlanSteps lists the steps of each recovery plan the plan
// phase stored.
func scanDRSRecoveryPlanSteps(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string, plans []childParent) (int, int, error) {
	return storeDRSChildren(ctx, st, acct, region, drsRecoveryPlanStepsOp, plans,
		func(ctx context.Context, arn string, token *string) ([]drstypes.RecoveryPlanStep, *string, error) {
			out, err := client.ListRecoveryPlanSteps(ctx, &drs.ListRecoveryPlanStepsInput{RecoveryPlanArn: &arn, NextToken: token, MaxResults: sdkaws.Int32(drsPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.RecoveryPlanSteps, out.NextToken, nil
		},
		func(plan childParent, step drstypes.RecoveryPlanStep) (*store.Resource, string, string) {
			arn := sv(step.RecoveryPlanStepArn)
			if arn == "" {
				return nil, "recovery plan steps have an empty ARN", plan.arn + " " + sv(step.StepName)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSRecoveryPlanStep, NativeID: arn, Name: step.StepName, Region: &region,
				AttributesJSON: mustJSON(flattenDRSRecoveryPlanStep(step)), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanDRSReplicationConfigurations reads the replication configuration of each
// source server the server phase stored. The response carries no ARN or id of
// its own, only the server's id, so the NativeID is
// {serverARN}/replication-configuration. There is no listed row to keep for a
// server whose read fails, so a transient failure is returned, after the other
// servers' rows are stored, and the scan reads as partial.
func scanDRSReplicationConfigurations(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string, servers []childParent) (int, int, error) {
	return storeDRSChildren(ctx, st, acct, region, drsReplicationConfigOp, servers,
		func(ctx context.Context, id string, _ *string) ([]*drs.GetReplicationConfigurationOutput, *string, error) {
			out, err := client.GetReplicationConfiguration(ctx, &drs.GetReplicationConfigurationInput{SourceServerID: &id})
			if err != nil {
				return nil, nil, err
			}
			return []*drs.GetReplicationConfigurationOutput{out}, nil, nil
		},
		func(server childParent, out *drs.GetReplicationConfigurationOutput) (*store.Resource, string, string) {
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSReplicationConfiguration, NativeID: server.arn + "/replication-configuration",
				Name: out.Name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}, "", ""
		})
}

// isDRSNotFound matches a recovery plan deleted between its list call and
// GetRecoveryPlan, which models ResourceNotFoundException.
func isDRSNotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// drsPlanKept describes the recovery plans of one cause stored as their list
// summary.
const drsPlanKept = "%d recovery plan(s) stored as their list summary, without Description or Tags; first %s: %w"

// drsPlanRead is one plan's GetRecoveryPlan result; both nil when the plan
// was not read.
type drsPlanRead struct {
	out *drs.GetRecoveryPlanOutput
	err error
}

// scanDRSRecoveryPlans lists the plans, then reads each, concurrently through
// readEachKey, with GetRecoveryPlan: only the full plan carries Description
// and Tags. A plan the read no longer finds was deleted since it was listed
// and is not stored. One whose read fails any other way, or that was not read
// because ctx ended, is stored from its list summary: a denial is warned about
// once, transient failures come back as one error (the rows lost data a
// rescan recovers), every other failure is returned after storing, and the
// plans never read add no error of their own beside the one that says ctx
// ended (reads in flight then fail with their own errors). A failed list page keeps the plans read before it: a denial
// is warned about, any other error returned. It also returns the stored plans
// for the step phase.
func scanDRSRecoveryPlans(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	summaries, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]drstypes.RecoveryPlanSummary, *string, error) {
		out, err := client.ListRecoveryPlans(ctx, &drs.ListRecoveryPlansInput{NextToken: token, MaxResults: sdkaws.Int32(drsPageMax)})
		if err != nil {
			return nil, nil, err
		}
		return out.RecoveryPlans, out.NextToken, nil
	})
	var (
		listed    []drstypes.RecoveryPlanSummary
		rows      []*store.Resource
		plans     []childParent
		errs      []error
		skips     notStoredSkips
		denied    skipTally
		transient transientTally
	)
	for _, s := range summaries {
		if sv(s.RecoveryPlanArn) == "" {
			skips.skipped("recovery plans have an empty ARN", sv(s.Name))
			continue
		}
		listed = append(listed, s)
	}
	reads, stopped := readEachKey(ctx, listed, func(ctx context.Context, s drstypes.RecoveryPlanSummary) drsPlanRead {
		out, err := client.GetRecoveryPlan(ctx, &drs.GetRecoveryPlanInput{RecoveryPlanArn: s.RecoveryPlanArn})
		return drsPlanRead{out, err}
	})
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", drsGetRecoveryPlanOp, stopped))
	}
	// A plan not read once ctx ended keeps its summary row.
	reads = append(reads, make([]drsPlanRead, len(listed)-len(reads))...)
	for i, s := range listed {
		arn, read := *s.RecoveryPlanArn, reads[i]
		r := &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDRSRecoveryPlanResource, NativeID: arn, Name: s.Name, Region: &region,
			Status: nonEmptyPtr(string(s.Status)), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
		}
		out, err := read.out, read.err
		switch {
		case out == nil && err == nil:
		case err == nil && out.RecoveryPlan != nil:
			p := out.RecoveryPlan
			r.Name, r.Status, r.AttributesJSON, r.TagsJSON = p.Name, nonEmptyPtr(string(p.Status)), mustJSON(p), mapTagsJSON(p.Tags)
		case err == nil:
			errs = append(errs, fmt.Errorf("%s %s: the response carries no recovery plan", drsGetRecoveryPlanOp, arn))
		case isDRSNotFound(err):
			continue
		case isAccessDenied(err):
			denied.skipped(arn, err)
		case transient.drop(arn, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", drsGetRecoveryPlanOp, arn, err))
		}
		rows = append(rows, r)
		plans = append(plans, childParent{id: arn, arn: arn})
	}
	if t := &transient.skipTally; t.count > 0 {
		errs = append(errs, fmt.Errorf("%s: "+drsPlanKept, drsGetRecoveryPlanOp, t.count, t.first, t.err))
	}
	total, inserted, err := storeReadRows(st, acct, region, drsRecoveryPlansOp, rows, listErr, func() {
		skips.warn(st, drsRecoveryPlansOp, acct, region)
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, drsGetRecoveryPlanOp, acct.ID, region, fmt.Errorf(drsPlanKept, denied.count, denied.first, denied.err))
		}
	}, errs...)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, nil, err
	}
	return total, inserted, plans, err
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
