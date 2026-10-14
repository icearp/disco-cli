package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssmincidents"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMIncidentsReplicationSet, Service: "ssm-incidents"})
	registerType(restype.Descriptor{Type: TypeSSMIncidentsResponsePlan, Service: "ssm-incidents"})
	registerType(restype.Descriptor{Type: TypeSSMIncidentsResourcePolicy, Service: "ssm-incidents"})
	registerService(serviceEntry{
		name: "aws:ssm-incidents",
		fn:   scanSSMIncidents,
	})
}

type ssmIncidentsAPI interface {
	ListReplicationSets(context.Context, *ssmincidents.ListReplicationSetsInput, ...func(*ssmincidents.Options)) (*ssmincidents.ListReplicationSetsOutput, error)
	GetReplicationSet(context.Context, *ssmincidents.GetReplicationSetInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetReplicationSetOutput, error)
	ListResponsePlans(context.Context, *ssmincidents.ListResponsePlansInput, ...func(*ssmincidents.Options)) (*ssmincidents.ListResponsePlansOutput, error)
	GetResponsePlan(context.Context, *ssmincidents.GetResponsePlanInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetResponsePlanOutput, error)
	GetResourcePolicies(context.Context, *ssmincidents.GetResourcePoliciesInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetResourcePoliciesOutput, error)
}

// scanSSMIncidents discovers Incident Manager replication sets, response plans
// and the resource policies attached to each response plan. Replication set is
// a per-account singleton: ListReplicationSets returns the ARN list,
// GetReplicationSet enriches.
func scanSSMIncidents(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmincidents.NewFromConfig(acct.cfg, func(o *ssmincidents.Options) { o.Region = region })

	t, i, ferr := scanSSMIReplicationSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, plans, ferr := scanSSMIResponsePlans(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSSMIResourcePolicies(ctx, client, acct, region, st, scanID, plans)
	total += t
	inserted += i
	return total, inserted, ferr
}

func scanSSMIReplicationSets(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListReplicationSets(ctx, &ssmincidents.ListReplicationSetsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm-incidents:ListReplicationSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm-incidents:ListReplicationSets: %w", err)
		}
		arns = append(arns, out.ReplicationSetArns...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	var batch []*store.Resource
	for _, arn := range arns {
		a := arn
		out, err := client.GetReplicationSet(ctx, &ssmincidents.GetReplicationSetInput{Arn: &a})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("ssm-incidents:GetReplicationSet %s: %w", a, err)
		}
		var status string
		if out.ReplicationSet != nil {
			status = string(out.ReplicationSet.Status)
		}
		label := a
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSSMIncidentsReplicationSet, NativeID: a,
			Name: &label, Region: &region, Status: &status,
			AttributesJSON: mustJSON(out.ReplicationSet), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "ssm-incidents replication-sets")
}

// scanSSMIResponsePlans stores the response plans and returns them as the
// parents of the resource-policy phase.
func scanSSMIResponsePlans(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var (
		batch []*store.Resource
		plans []childParent
	)
	var nextToken *string
	for {
		out, err := client.ListResponsePlans(ctx, &ssmincidents.ListResponsePlansInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "ssm-incidents:ListResponsePlans", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("ssm-incidents:ListResponsePlans: %w", err)
		}
		for _, p := range out.ResponsePlanSummaries {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			// Enrich with GetResponsePlan body — Engagements (ssm-contacts ARNs)
			// and other refs are not on the list-summary shape. Fall back to
			// summary on per-row failure.
			attrs := mustJSON(p)
			parn := arn
			gout, gerr := client.GetResponsePlan(ctx, &ssmincidents.GetResponsePlanInput{Arn: &parn})
			if gerr != nil {
				if isAccessDenied(gerr) {
					_ = skipIfAccessDenied(st, "ssm-incidents:GetResponsePlan", acct.ID, region, gerr)
				}
			} else if gout != nil {
				attrs = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMIncidentsResponsePlan, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
			plans = append(plans, childParent{id: arn, arn: arn})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "ssm-incidents response-plans")
	if err != nil {
		return total, inserted, nil, err
	}
	return total, inserted, plans, nil
}

// ssmIResourcePolicyNativeID keys a policy under its response plan: the API
// returns no ARN for a policy and the service reference defines none, and the
// SDK documents no scope in which a PolicyId is unique.
func ssmIResourcePolicyNativeID(planARN, policyID string) string {
	return planARN + "/resource-policy/" + policyID
}

// isSSMINotFound reports a response plan deleted between its listing and the
// GetResourcePolicies call.
func isSSMINotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanSSMIResourcePolicies lists the resource policies that share each response
// plan stored this scan through RAM, and stores each under its plan.
func scanSSMIResourcePolicies(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string, plans []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ssm-incidents:GetResourcePolicies", plans, isSSMINotFound,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := ssmincidents.NewGetResourcePoliciesPaginator(client, &ssmincidents.GetResourcePoliciesInput{ResourceArn: &p.arn})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, pol := range out.ResourcePolicies {
					id := sv(pol.PolicyId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSMIncidentsResourcePolicy, NativeID: ssmIResourcePolicyNativeID(p.arn, id),
						Name: pol.PolicyId, Region: &region,
						AttributesJSON: mustJSON(pol), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}
