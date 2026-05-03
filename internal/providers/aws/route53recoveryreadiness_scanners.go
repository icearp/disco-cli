package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness"
)

func init() {
	registerService(serviceEntry{
		name: "aws:route53-recovery-readiness",
		fn:   scanR53RecoveryReadiness,
		emits: []coverage.TypeDecl{
			{Service: "route53-recovery-readiness", DiscoType: TypeR53RRCell},
			{Service: "route53-recovery-readiness", DiscoType: TypeR53RRReadinessCheck},
			{Service: "route53-recovery-readiness", DiscoType: TypeR53RRRecoveryGroup},
			{Service: "route53-recovery-readiness", DiscoType: TypeR53RRResourceSet},
		},
	})
}

type r53rrAPI interface {
	ListCells(context.Context, *route53recoveryreadiness.ListCellsInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListCellsOutput, error)
	ListReadinessChecks(context.Context, *route53recoveryreadiness.ListReadinessChecksInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListReadinessChecksOutput, error)
	ListRecoveryGroups(context.Context, *route53recoveryreadiness.ListRecoveryGroupsInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListRecoveryGroupsOutput, error)
	ListResourceSets(context.Context, *route53recoveryreadiness.ListResourceSetsInput, ...func(*route53recoveryreadiness.Options)) (*route53recoveryreadiness.ListResourceSetsOutput, error)
}

// scanR53RecoveryReadiness discovers Route53 Recovery Readiness cells,
// readiness checks, recovery groups, and resource sets via paginated List*
// calls. ARNs are native on every type. Service is global with a single
// us-west-2 endpoint — gate so multi-region scans skip the DNS-lookup
// failures that otherwise warn from every other region.
func scanR53RecoveryReadiness(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if region != "us-west-2" {
		return 0, 0, nil
	}
	client := route53recoveryreadiness.NewFromConfig(acct.cfg, func(o *route53recoveryreadiness.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanR53RRCells(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RRReadinessChecks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RRRecoveryGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RRResourceSets(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanR53RRCells(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53recoveryreadiness.NewListCellsPaginator(client, &route53recoveryreadiness.ListCellsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoveryreadiness:ListCells", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoveryreadiness:ListCells: %w", err)
		}
		for _, c := range out.Cells {
			arn := sv(c.CellArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RRCell, NativeID: arn,
				Name: c.CellName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rr cells")
}

func scanR53RRReadinessChecks(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53recoveryreadiness.NewListReadinessChecksPaginator(client, &route53recoveryreadiness.ListReadinessChecksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoveryreadiness:ListReadinessChecks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoveryreadiness:ListReadinessChecks: %w", err)
		}
		for _, c := range out.ReadinessChecks {
			arn := sv(c.ReadinessCheckArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RRReadinessCheck, NativeID: arn,
				Name: c.ReadinessCheckName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rr readiness-checks")
}

func scanR53RRRecoveryGroups(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53recoveryreadiness.NewListRecoveryGroupsPaginator(client, &route53recoveryreadiness.ListRecoveryGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoveryreadiness:ListRecoveryGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoveryreadiness:ListRecoveryGroups: %w", err)
		}
		for _, g := range out.RecoveryGroups {
			arn := sv(g.RecoveryGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RRRecoveryGroup, NativeID: arn,
				Name: g.RecoveryGroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rr recovery-groups")
}

func scanR53RRResourceSets(ctx context.Context, client r53rrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53recoveryreadiness.NewListResourceSetsPaginator(client, &route53recoveryreadiness.ListResourceSetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53recoveryreadiness:ListResourceSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("route53recoveryreadiness:ListResourceSets: %w", err)
		}
		for _, rs := range out.ResourceSets {
			arn := sv(rs.ResourceSetArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53RRResourceSet, NativeID: arn,
				Name: rs.ResourceSetName, Region: &region,
				AttributesJSON: mustJSON(rs), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "r53rr resource-sets")
}
