package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssmincidents"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMIncidentsReplicationSet, Service: "ssm-incidents", Upstream: "AWS::SSMIncidents::ReplicationSet"})
	registerType(restype.Descriptor{Type: TypeSSMIncidentsResponsePlan, Service: "ssm-incidents", Upstream: "AWS::SSMIncidents::ResponsePlan"})
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
}

// scanSSMIncidents discovers Incident Manager replication sets and response
// plans. Replication set is a per-account singleton: ListReplicationSets
// returns the ARN list, GetReplicationSet enriches.
func scanSSMIncidents(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmincidents.NewFromConfig(acct.cfg, func(o *ssmincidents.Options) { o.Region = region })

	t, i, ferr := scanSSMIReplicationSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSSMIResponsePlans(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
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

func scanSSMIResponsePlans(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListResponsePlans(ctx, &ssmincidents.ListResponsePlansInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm-incidents:ListResponsePlans", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm-incidents:ListResponsePlans: %w", err)
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
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ssm-incidents response-plans")
}
