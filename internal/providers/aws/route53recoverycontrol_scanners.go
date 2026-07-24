package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig"
)

func init() {
	registerType(restype.Descriptor{Type: TypeR53RCCluster, Service: "route53-recovery-control", Leaf: true})
	registerType(restype.Descriptor{Type: TypeR53RCControlPanel, Service: "route53-recovery-control", Leaf: true})
	registerType(restype.Descriptor{Type: TypeR53RCRoutingControl, Service: "route53-recovery-control", Leaf: true})
	registerType(restype.Descriptor{Type: TypeR53RCSafetyRule, Service: "route53-recovery-control", Leaf: true})
	registerService(serviceEntry{
		name:   "aws:route53-recovery-control",
		global: true,
		fn:     scanR53RecoveryControl,
	})
}

type r53rcAPI interface {
	ListClusters(context.Context, *route53recoverycontrolconfig.ListClustersInput, ...func(*route53recoverycontrolconfig.Options)) (*route53recoverycontrolconfig.ListClustersOutput, error)
	ListControlPanels(context.Context, *route53recoverycontrolconfig.ListControlPanelsInput, ...func(*route53recoverycontrolconfig.Options)) (*route53recoverycontrolconfig.ListControlPanelsOutput, error)
	ListRoutingControls(context.Context, *route53recoverycontrolconfig.ListRoutingControlsInput, ...func(*route53recoverycontrolconfig.Options)) (*route53recoverycontrolconfig.ListRoutingControlsOutput, error)
	ListSafetyRules(context.Context, *route53recoverycontrolconfig.ListSafetyRulesInput, ...func(*route53recoverycontrolconfig.Options)) (*route53recoverycontrolconfig.ListSafetyRulesOutput, error)
}

// scanR53RecoveryControl discovers Route53 Recovery Control clusters, control panels,
// routing controls, and safety rules (routing controls and safety rules fan out per
// control panel). Global service, single us-west-2 endpoint — gated to skip the
// DNS-lookup-failure warnings other regions would otherwise raise.
func scanR53RecoveryControl(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-west-2"
	client := route53recoverycontrolconfig.NewFromConfig(acct.cfg, func(o *route53recoverycontrolconfig.Options) { o.Region = region })

	t, i, ferr := scanR53RCClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	cpARNs, t, i, ferr := scanR53RCControlPanels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, cp := range cpARNs {
		t, i, ferr = scanR53RCRoutingControls(ctx, client, acct, region, st, scanID, cp)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanR53RCSafetyRules(ctx, client, acct, region, st, scanID, cp)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanR53RCClusters(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53recoverycontrolconfig.NewListClustersPaginator(client, &route53recoverycontrolconfig.ListClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoverycontrolconfig:ListClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoverycontrolconfig:ListClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ClusterArn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RCCluster, NativeID: arn,
				Name: c.Name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rc clusters")
}

func scanR53RCControlPanels(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := route53recoverycontrolconfig.NewListControlPanelsPaginator(client, &route53recoverycontrolconfig.ListControlPanelsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "route53recoverycontrolconfig:ListControlPanels", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("route53recoverycontrolconfig:ListControlPanels: %w", err)
		}
		for _, c := range out.ControlPanels {
			arn := sv(c.ControlPanelArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RCControlPanel, NativeID: arn,
				Name: c.Name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "r53rc control-panels")
	return arns, t, i, err
}

func scanR53RCRoutingControls(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string, cpARN string) (int, int, error) {
	cp := cpARN
	pager := route53recoverycontrolconfig.NewListRoutingControlsPaginator(client, &route53recoverycontrolconfig.ListRoutingControlsInput{ControlPanelArn: &cp})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoverycontrolconfig:ListRoutingControls", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoverycontrolconfig:ListRoutingControls: %w", err)
		}
		for _, rc := range out.RoutingControls {
			arn := sv(rc.RoutingControlArn)
			if arn == "" {
				continue
			}
			status := string(rc.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RCRoutingControl, NativeID: arn,
				Name: rc.Name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(rc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rc routing-controls")
}

// scanR53RCSafetyRules walks the union Rule type, extracting ARN/Name from
// whichever subtype (ASSERTION or GATING) is populated.
func scanR53RCSafetyRules(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string, cpARN string) (int, int, error) {
	cp := cpARN
	pager := route53recoverycontrolconfig.NewListSafetyRulesPaginator(client, &route53recoverycontrolconfig.ListSafetyRulesInput{ControlPanelArn: &cp})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoverycontrolconfig:ListSafetyRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoverycontrolconfig:ListSafetyRules: %w", err)
		}
		for _, r := range out.SafetyRules {
			var arn *string
			var name *string
			var status string
			if r.ASSERTION != nil {
				arn = r.ASSERTION.SafetyRuleArn
				name = r.ASSERTION.Name
				status = string(r.ASSERTION.Status)
			} else if r.GATING != nil {
				arn = r.GATING.SafetyRuleArn
				name = r.GATING.Name
				status = string(r.GATING.Status)
			}
			if sv(arn) == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RCSafetyRule, NativeID: sv(arn),
				Name: name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rc safety-rules")
}
