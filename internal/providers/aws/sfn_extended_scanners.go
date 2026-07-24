package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

// scanSFNStateMachineAliasesAndVersions discovers aliases and versions for
// every standard-tier state machine. ListStateMachineAliases /
// ListStateMachineVersions reject EXPRESS state-machine ARNs with
// ValidationException — tolerated.
func scanSFNStateMachineAliasesAndVersions(ctx context.Context, client sfnAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var smARNs []string
	pager := sfn.NewListStateMachinesPaginator(client, &sfn.ListStateMachinesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "states:ListStateMachines(extended)", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("states:ListStateMachines(extended): %w", err)
		}
		for _, sm := range out.StateMachines {
			if a := sv(sm.StateMachineArn); a != "" {
				smARNs = append(smARNs, a)
			}
		}
	}

	t, i, ferr := scanSFNAliases(ctx, client, smARNs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSFNVersions(ctx, client, smARNs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSFNAliases(ctx context.Context, client sfnAPI, smARNs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, smARN := range smARNs {
		arn := smARN
		var nextToken *string
		for {
			out, err := client.ListStateMachineAliases(ctx, &sfn.ListStateMachineAliasesInput{
				StateMachineArn: &arn,
				NextToken:       nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "states:ListStateMachineAliases", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ValidationException", "StateMachineDoesNotExist", "StateMachineTypeNotSupported") {
					break
				}
				return 0, 0, fmt.Errorf("states:ListStateMachineAliases sm=%s: %w", arn, err)
			}
			for _, a := range out.StateMachineAliases {
				aliasArn := sv(a.StateMachineAliasArn)
				if aliasArn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSFNStateMachineAlias, NativeID: aliasArn,
					Region:         &region,
					AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "stepfunctions state-machine-aliases")
}

func scanSFNVersions(ctx context.Context, client sfnAPI, smARNs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, smARN := range smARNs {
		arn := smARN
		var nextToken *string
		for {
			out, err := client.ListStateMachineVersions(ctx, &sfn.ListStateMachineVersionsInput{
				StateMachineArn: &arn,
				NextToken:       nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "states:ListStateMachineVersions", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ValidationException", "StateMachineDoesNotExist", "StateMachineTypeNotSupported") {
					break
				}
				return 0, 0, fmt.Errorf("states:ListStateMachineVersions sm=%s: %w", arn, err)
			}
			for _, v := range out.StateMachineVersions {
				varn := sv(v.StateMachineVersionArn)
				if varn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSFNStateMachineVersion, NativeID: varn,
					Region:         &region,
					AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "stepfunctions state-machine-versions")
}
