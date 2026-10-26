package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness"
	r53rrtypes "github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeR53RRCell, Service: "route53-recovery-readiness"})
	registerType(restype.Descriptor{Type: TypeR53RRReadinessCheck, Service: "route53-recovery-readiness"})
	registerType(restype.Descriptor{Type: TypeR53RRRecoveryGroup, Service: "route53-recovery-readiness"})
	registerType(restype.Descriptor{Type: TypeR53RRResourceSet, Service: "route53-recovery-readiness"})
	registerService(serviceEntry{
		name:   "aws:route53-recovery-readiness",
		global: true,
		fn:     scanR53RecoveryReadiness,
	})
}

const (
	r53rrListCellsOp           = "route53recoveryreadiness:ListCells"
	r53rrListReadinessChecksOp = "route53recoveryreadiness:ListReadinessChecks"
	r53rrListRecoveryGroupsOp  = "route53recoveryreadiness:ListRecoveryGroups"
	r53rrListResourceSetsOp    = "route53recoveryreadiness:ListResourceSets"
)

// r53rrPageMax is the MaxResults cap every List op here models.
const r53rrPageMax int32 = 1000

type r53rrAPI interface {
	ListCells(context.Context, *route53recoveryreadiness.ListCellsInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListCellsOutput, error)
	ListReadinessChecks(context.Context, *route53recoveryreadiness.ListReadinessChecksInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListReadinessChecksOutput, error)
	ListRecoveryGroups(context.Context, *route53recoveryreadiness.ListRecoveryGroupsInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListRecoveryGroupsOutput, error)
	ListResourceSets(context.Context, *route53recoveryreadiness.ListResourceSetsInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListResourceSetsOutput, error)
}

// scanR53RecoveryReadiness discovers Route53 Recovery Readiness cells, readiness
// checks, recovery groups, and resource sets; ARNs are native on every type.
// The service is account-wide; for aws accounts its only endpoint is in
// us-west-2, so it is registered global and the client is pinned to its home
// region (homeRegion).
func scanR53RecoveryReadiness(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("route53recoveryreadiness")
	if err != nil {
		return 0, 0, err
	}
	client := route53recoveryreadiness.NewFromConfig(acct.cfg, func(o *route53recoveryreadiness.Options) { o.Region = region })
	return scanR53RRAll(ctx, client, acct, region, st, scanID)
}

// scanR53RRAll runs every listing and joins their errors; the listings are
// independent, so only a store write failure stops the rest.
func scanR53RRAll(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var p phaseTotals
	for _, ph := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{r53rrListCellsOp, func() (int, int, error) { return scanR53RRCells(ctx, client, acct, region, st, scanID) }},
		{r53rrListReadinessChecksOp, func() (int, int, error) { return scanR53RRReadinessChecks(ctx, client, acct, region, st, scanID) }},
		{r53rrListRecoveryGroupsOp, func() (int, int, error) { return scanR53RRRecoveryGroups(ctx, client, acct, region, st, scanID) }},
		{r53rrListResourceSetsOp, func() (int, int, error) { return scanR53RRResourceSets(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// r53rrRow builds the row of a readiness resource, or returns why it cannot be
// stored (what names the resources, field the ARN member) keyed by its name
// when the ARN is empty.
func r53rrRow(acct *account, scanID, rtype, what, field string, arn, name *string, attrs any, tags map[string]string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty " + field, sv(name)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: name, Region: regionGlobal,
		AttributesJSON: mustJSON(attrs), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

func scanR53RRCells(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, r53rrListCellsOp,
		func(ctx context.Context, token *string) ([]r53rrtypes.CellOutput, *string, error) {
			out, err := client.ListCells(ctx, &route53recoveryreadiness.ListCellsInput{NextToken: token, MaxResults: sdkaws.Int32(r53rrPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.Cells, out.NextToken, nil
		},
		func(c r53rrtypes.CellOutput) (*store.Resource, string, string) {
			return r53rrRow(acct, scanID, TypeR53RRCell, "cells", "CellArn", c.CellArn, c.CellName, c, c.Tags)
		})
}

func scanR53RRReadinessChecks(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, r53rrListReadinessChecksOp,
		func(ctx context.Context, token *string) ([]r53rrtypes.ReadinessCheckOutput, *string, error) {
			out, err := client.ListReadinessChecks(ctx, &route53recoveryreadiness.ListReadinessChecksInput{NextToken: token, MaxResults: sdkaws.Int32(r53rrPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.ReadinessChecks, out.NextToken, nil
		},
		func(c r53rrtypes.ReadinessCheckOutput) (*store.Resource, string, string) {
			return r53rrRow(acct, scanID, TypeR53RRReadinessCheck, "readiness checks", "ReadinessCheckArn", c.ReadinessCheckArn, c.ReadinessCheckName, c, c.Tags)
		})
}

func scanR53RRRecoveryGroups(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, r53rrListRecoveryGroupsOp,
		func(ctx context.Context, token *string) ([]r53rrtypes.RecoveryGroupOutput, *string, error) {
			out, err := client.ListRecoveryGroups(ctx, &route53recoveryreadiness.ListRecoveryGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(r53rrPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.RecoveryGroups, out.NextToken, nil
		},
		func(g r53rrtypes.RecoveryGroupOutput) (*store.Resource, string, string) {
			return r53rrRow(acct, scanID, TypeR53RRRecoveryGroup, "recovery groups", "RecoveryGroupArn", g.RecoveryGroupArn, g.RecoveryGroupName, g, g.Tags)
		})
}

func scanR53RRResourceSets(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, r53rrListResourceSetsOp,
		func(ctx context.Context, token *string) ([]r53rrtypes.ResourceSetOutput, *string, error) {
			out, err := client.ListResourceSets(ctx, &route53recoveryreadiness.ListResourceSetsInput{NextToken: token, MaxResults: sdkaws.Int32(r53rrPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.ResourceSets, out.NextToken, nil
		},
		func(rs r53rrtypes.ResourceSetOutput) (*store.Resource, string, string) {
			return r53rrRow(acct, scanID, TypeR53RRResourceSet, "resource sets", "ResourceSetArn", rs.ResourceSetArn, rs.ResourceSetName, rs, rs.Tags)
		})
}
