package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ssmincidents"
)

func init() {
	registerService(serviceEntry{
		name: "aws:ssm-incidents",
		fn:   scanSSMIncidents,
		emits: []coverage.TypeDecl{
			{Service: "ssm-incidents", DiscoType: TypeSSMIncidentsReplicationSet},
			{Service: "ssm-incidents", DiscoType: TypeSSMIncidentsResponsePlan},
		},
	})
}

type ssmIncidentsAPI interface {
	ListReplicationSets(context.Context, *ssmincidents.ListReplicationSetsInput, ...func(*ssmincidents.Options)) (*ssmincidents.ListReplicationSetsOutput, error)
	GetReplicationSet(context.Context, *ssmincidents.GetReplicationSetInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetReplicationSetOutput, error)
	ListResponsePlans(context.Context, *ssmincidents.ListResponsePlansInput, ...func(*ssmincidents.Options)) (*ssmincidents.ListResponsePlansOutput, error)
}

// scanSSMIncidents discovers Incident Manager replication sets and response
// plans. Replication set is a per-account singleton; ListReplicationSets
// returns ARN list, GetReplicationSet enriches.
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMIncidentsResponsePlan, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ssm-incidents response-plans")
}
