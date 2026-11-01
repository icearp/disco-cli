package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/icearp/disco-cli/store"
)

const (
	sfnListAliasesOp  = "states:ListStateMachineAliases"
	sfnListVersionsOp = "states:ListStateMachineVersions"
)

// sfnAliasesGone is the codes ListStateMachineAliases models for a state
// machine deleted, or being deleted, since it was listed.
var sfnAliasesGone = []string{"ResourceNotFound", "StateMachineDoesNotExist", "StateMachineDeleting"}

// scanSFNAliases stores the aliases of each state machine the first phase
// stored; one state machine's failure costs the others none of theirs
// (storeEachParentPages). It reports whether a write failed, after which the
// scan must stop.
func scanSFNAliases(ctx context.Context, client sfnAPI, acct *account, region string, st *store.Store, scanID string, machines []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, sfnListAliasesOp, sfnAliasesGone, machines,
		func(ctx context.Context, smARN string, token *string) ([]sfntypes.StateMachineAliasListItem, *string, error) {
			out, err := client.ListStateMachineAliases(ctx, &sfn.ListStateMachineAliasesInput{StateMachineArn: &smARN, MaxResults: sfnPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StateMachineAliases, out.NextToken, nil
		},
		func(smARN string, a sfntypes.StateMachineAliasListItem) (*store.Resource, string, string) {
			return sfnChildRow(acct, region, scanID, TypeSFNStateMachineAlias, "state machine aliases", a.StateMachineAliasArn, "an alias of "+smARN, a)
		}, p)
}

// scanSFNVersions stores the versions of each state machine the first phase
// stored; one state machine's failure costs the others none of theirs
// (storeEachParentPages). ListStateMachineVersions models no not-found, so
// any error for a state machine deleted since it was listed is returned (not
// verified against a live account). It reports whether a write failed, after
// which the scan must stop.
func scanSFNVersions(ctx context.Context, client sfnAPI, acct *account, region string, st *store.Store, scanID string, machines []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, sfnListVersionsOp, nil, machines,
		func(ctx context.Context, smARN string, token *string) ([]sfntypes.StateMachineVersionListItem, *string, error) {
			out, err := client.ListStateMachineVersions(ctx, &sfn.ListStateMachineVersionsInput{StateMachineArn: &smARN, MaxResults: sfnPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StateMachineVersions, out.NextToken, nil
		},
		func(smARN string, v sfntypes.StateMachineVersionListItem) (*store.Resource, string, string) {
			return sfnChildRow(acct, region, scanID, TypeSFNStateMachineVersion, "state machine versions", v.StateMachineVersionArn, "a version of "+smARN, v)
		}, p)
}

// sfnChildRow is the row of a listed alias or version, stored under its ARN,
// with key naming an element without one (arnRow).
func sfnChildRow(acct *account, region, scanID, typ, what string, arn *string, key string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, &key, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}
