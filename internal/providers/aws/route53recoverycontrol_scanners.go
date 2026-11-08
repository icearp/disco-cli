package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig"
	r53rctypes "github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeR53RCCluster, Service: "route53-recovery-control"})
	registerType(restype.Descriptor{Type: TypeR53RCControlPanel, Service: "route53-recovery-control"})
	registerType(restype.Descriptor{Type: TypeR53RCRoutingControl, Service: "route53-recovery-control", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeR53RCSafetyRule, Service: "route53-recovery-control"})
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
	ListTagsForResource(context.Context, *route53recoverycontrolconfig.ListTagsForResourceInput, ...func(*route53recoverycontrolconfig.Options)) (*route53recoverycontrolconfig.ListTagsForResourceOutput, error)
}

const (
	r53rcTagsOp = "route53recoverycontrolconfig:ListTagsForResource"
	// r53rcPageCap is the MaxResults every listing sends: the maximum of the
	// range the model gives all four (1-1000).
	r53rcPageCap = 1000
)

// r53rcGone is the not-found code ListRoutingControls, ListSafetyRules and
// ListTagsForResource model: the control panel a child listing names, or the
// resource whose tags are read, was deleted since it was listed.
var r53rcGone = []string{"ResourceNotFoundException"}

// scanR53RecoveryControl discovers Route53 Recovery Control clusters, control
// panels, and each control panel's routing controls and safety rules. Global
// service with a single endpoint, its home region (homeRegion), us-west-2 for
// aws accounts; every call, the tag reads included, goes through the one
// client built for it. Every listing runs as its own phase, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The child phases read the control panels stored this scan,
// those panels concurrently. Clusters, control panels and safety rules have
// their tags read per row (r53rcTags), whose denials are one warning for the
// whole scan.
func scanR53RecoveryControl(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, error) {
	region, err := acct.homeRegion("route53recoverycontrolconfig")
	if err != nil {
		return 0, err
	}
	client := route53recoverycontrolconfig.NewFromConfig(acct.cfg, func(o *route53recoverycontrolconfig.Options) { o.Region = region })
	tags := r53rcTags(client, acct.ID).sharingDenials()
	var panels []childParent
	total, err := runHostPhases(ctx, st, acct, region, "route53recoverycontrolconfig", 0, []hostPhase{
		{op: "route53recoverycontrolconfig:ListClusters", run: func(p *phaseTotals) bool {
			return scanR53RCClusters(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "route53recoverycontrolconfig:ListControlPanels", run: func(p *phaseTotals) (failed bool) {
			panels, failed = scanR53RCControlPanels(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "route53recoverycontrolconfig:ListRoutingControls", run: func(p *phaseTotals) bool {
			return scanR53RCRoutingControls(ctx, client, acct, region, st, scanID, panels, p)
		}},
		{op: "route53recoverycontrolconfig:ListSafetyRules", run: func(p *phaseTotals) bool {
			return scanR53RCSafetyRules(ctx, client, acct, region, st, scanID, panels, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// r53rcTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists
// cluster, controlpanel and safetyrule under ListTagsForResource and
// TagResource, and each one's Create* input carries tags; routing controls
// have neither, so theirs are not read. No listed element carries tags, and
// the op does not page them.
//
// Every element names its Owner account, and a cluster can carry a resource
// policy (GetResourcePolicy), the means of sharing it with other accounts, so
// a listing may return a resource another account owns. A row whose ARN names
// another account gives no tags and no error on a denial or not-found
// (untaggedIfOtherAccount). Whether shared resources are listed, and whether
// the service refuses their tag reads, is unverified live.
func r53rcTags(client r53rcAPI, acctID string) rowTagReader {
	return rowTagReader{
		op:       r53rcTagsOp,
		notFound: r53rcGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &route53recoverycontrolconfig.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}.untaggedIfOtherAccount(acctID)
}

// r53rcRow is the row of a listed element: stored under its ARN, with its
// name and status, and the element verbatim as the attributes. An element
// without an ARN is not stored (arnRow); what names such elements in the
// warning, keyed by key.
func r53rcRow(acct *account, scanID, typ, what string, arn, name *string, status r53rctypes.Status, key string, element any) (*store.Resource, string, string) {
	s := string(status)
	return arnRow(what, arn, &key, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: regionGlobal, Status: &s,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanR53RCClusters stores the clusters with their tags, a page at a time. It
// reports whether a write failed, after which the scan must stop.
func scanR53RCClusters(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "route53recoverycontrolconfig:ListClusters",
		func(ctx context.Context, token *string) ([]r53rctypes.Cluster, *string, error) {
			out, err := client.ListClusters(ctx, &route53recoverycontrolconfig.ListClustersInput{MaxResults: sdkaws.Int32(r53rcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Clusters, out.NextToken, nil
		},
		func(c r53rctypes.Cluster) (*store.Resource, string, string) {
			return r53rcRow(acct, scanID, TypeR53RCCluster, "clusters", c.ClusterArn, c.Name, c.Status, sv(c.Name), c)
		},
		tags, p)
}

// scanR53RCControlPanels stores every control panel of the account, those of
// all its clusters, with their tags, a page at a time, and returns the stored
// ones, the parents of the routing control and safety rule phases, each keyed
// by its ARN. The default control panel the service creates with each cluster
// is stored and tag-read like any other: the Service Reference makes no
// exception for it, and its name and routing controls are the account's. It
// reports whether a write failed, after which the scan must stop.
func scanR53RCControlPanels(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "route53recoverycontrolconfig:ListControlPanels", "control panels", "ControlPanelArn",
		func(ctx context.Context, token *string) ([]r53rctypes.ControlPanel, *string, error) {
			out, err := client.ListControlPanels(ctx, &route53recoverycontrolconfig.ListControlPanelsInput{MaxResults: sdkaws.Int32(r53rcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ControlPanels, out.NextToken, nil
		},
		func(c r53rctypes.ControlPanel) (*store.Resource, string, string) {
			return r53rcRow(acct, scanID, TypeR53RCControlPanel, "control panels", c.ControlPanelArn, c.Name, c.Status, sv(c.Name), c)
		},
		func(c r53rctypes.ControlPanel) string { return sv(c.ControlPanelArn) },
		tags, p)
}

// scanR53RCRoutingControls stores the routing controls of each control panel
// stored this scan, each page written as it is read; one panel's failure
// costs the others none of theirs. It reports whether a write failed, after
// which the scan must stop.
func scanR53RCRoutingControls(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string, panels []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "route53recoverycontrolconfig:ListRoutingControls", r53rcGone, panels,
		func(ctx context.Context, panelARN string, token *string) ([]r53rctypes.RoutingControl, *string, error) {
			out, err := client.ListRoutingControls(ctx, &route53recoverycontrolconfig.ListRoutingControlsInput{ControlPanelArn: &panelARN, MaxResults: sdkaws.Int32(r53rcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.RoutingControls, out.NextToken, nil
		},
		func(panelARN string, rc r53rctypes.RoutingControl) (*store.Resource, string, string) {
			return r53rcRow(acct, scanID, TypeR53RCRoutingControl, "routing controls", rc.RoutingControlArn, rc.Name, rc.Status, cmp.Or(sv(rc.Name), panelARN), rc)
		},
		p)
}

// scanR53RCSafetyRules stores the safety rules of each control panel stored
// this scan with their tags, each page written as soon as its tags are read;
// one panel's failure costs the others none of theirs. A listed rule is a
// union: its ARN, name and status come from whichever of ASSERTION and
// GATING is set, and a rule with neither is not stored, with one warning. It
// reports whether a write failed, after which the scan must stop.
func scanR53RCSafetyRules(ctx context.Context, client r53rcAPI, acct *account, region string, st *store.Store, scanID string, panels []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "route53recoverycontrolconfig:ListSafetyRules", r53rcGone, panels,
		func(ctx context.Context, panelARN string, token *string) ([]r53rctypes.Rule, *string, error) {
			out, err := client.ListSafetyRules(ctx, &route53recoverycontrolconfig.ListSafetyRulesInput{ControlPanelArn: &panelARN, MaxResults: sdkaws.Int32(r53rcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SafetyRules, out.NextToken, nil
		},
		func(panelARN string, r r53rctypes.Rule) (*store.Resource, string, string) {
			var (
				arn, name *string
				status    r53rctypes.Status
			)
			switch {
			case r.ASSERTION != nil:
				arn, name, status = r.ASSERTION.SafetyRuleArn, r.ASSERTION.Name, r.ASSERTION.Status
			case r.GATING != nil:
				arn, name, status = r.GATING.SafetyRuleArn, r.GATING.Name, r.GATING.Status
			}
			return r53rcRow(acct, scanID, TypeR53RCSafetyRule, "safety rules", arn, name, status, cmp.Or(sv(name), panelARN), r)
		},
		tags, p)
}
