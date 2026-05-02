package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/shield"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:shield",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanShield(ctx, acct, st, scanID)
		},
		emits: []coverage.TypeDecl{
			{Service: "shield", DiscoType: TypeShieldProtection},
			{Service: "shield", DiscoType: TypeShieldProtectionGroup},
			{Service: "shield", DiscoType: TypeShieldDRTAccess},
			{Service: "shield", DiscoType: TypeShieldProactiveEngagement},
		},
	})
}

// shieldAPI is the narrow set of Shield operations called by the scanShield
// sub-phases.
type shieldAPI interface {
	DescribeSubscription(context.Context, *shield.DescribeSubscriptionInput, ...func(*shield.Options)) (*shield.DescribeSubscriptionOutput, error)
	ListProtections(context.Context, *shield.ListProtectionsInput, ...func(*shield.Options)) (*shield.ListProtectionsOutput, error)
	ListProtectionGroups(context.Context, *shield.ListProtectionGroupsInput, ...func(*shield.Options)) (*shield.ListProtectionGroupsOutput, error)
	DescribeDRTAccess(context.Context, *shield.DescribeDRTAccessInput, ...func(*shield.Options)) (*shield.DescribeDRTAccessOutput, error)
	DescribeEmergencyContactSettings(context.Context, *shield.DescribeEmergencyContactSettingsInput, ...func(*shield.Options)) (*shield.DescribeEmergencyContactSettingsOutput, error)
}

// scanShield discovers Shield Advanced protections and protection groups.
// Shield is a global service; the client always uses us-east-1. A phase-0
// DescribeSubscription gate detects accounts without an active Advanced
// subscription (ResourceNotFoundException) and short-circuits via
// markServiceDisabled. Per-phase isShieldNotSubscribed skips remain as a
// safety net for the rare partial-IAM-grant case.
func scanShield(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := shield.NewFromConfig(acct.cfg, func(o *shield.Options) { o.Region = "us-east-1" })

	// Phase 0: subscription gate (no resource upsert — config, not a resource).
	if ferr := gateShieldSubscription(ctx, client, acct, st); ferr != nil {
		return 0, 0, ferr
	}

	// Phase 2: protections.
	{
		t, i, ferr := scanShieldProtections(ctx, client, acct, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 3: protection groups.
	{
		t, i, ferr := scanShieldProtectionGroups(ctx, client, acct, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 4: DRT access + proactive engagement (per-account singletons).
	{
		t, i, ferr := scanShieldDRTAccess(ctx, client, acct, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	{
		t, i, ferr := scanShieldProactiveEngagement(ctx, client, acct, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// isShieldNotSubscribed reports whether err indicates the account does not
// have an active Shield Advanced subscription. Shield's List/Describe APIs
// surface this as ResourceNotFoundException with a "not subscribed" message;
// treat it as a soft skip identical to AccessDenied so non-Advanced accounts
// do not pollute scan errors.
func isShieldNotSubscribed(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// gateShieldSubscription detects whether the account has an active Shield
// Advanced subscription. Accounts without one surface ResourceNotFoundException
// — returned wrapped via markServiceDisabled so the dispatcher renders
// "(service disabled)" rather than an error. The subscription itself is
// account-wide config, not an ARN'd resource, so no row is upserted.
func gateShieldSubscription(ctx context.Context, client shieldAPI, acct *account, st *store.Store) error {
	if _, derr := client.DescribeSubscription(ctx, &shield.DescribeSubscriptionInput{}); derr != nil {
		if isAccessDenied(derr) {
			_ = skipIfAccessDenied(st, "shield:DescribeSubscription", acct.ID, "", derr)
			return nil
		}
		if isShieldNotSubscribed(derr) {
			return markServiceDisabled(derr)
		}
		return fmt.Errorf("shield:DescribeSubscription: %w", derr)
	}
	return nil
}

func scanShieldProtections(ctx context.Context, client shieldAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := shield.NewListProtectionsPaginator(client, &shield.ListProtectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "shield:ListProtections", acct.ID, "", perr)
				return 0, 0, nil
			}
			if isShieldNotSubscribed(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("shield:ListProtections: %w", perr)
		}
		for _, p := range out.Protections {
			arn := sv(p.ProtectionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeShieldProtection,
				NativeID:       arn,
				Name:           p.Name,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert shield protections: %w", uerr)
	}
	return len(batch), n, nil
}

func scanShieldProtectionGroups(ctx context.Context, client shieldAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := shield.NewListProtectionGroupsPaginator(client, &shield.ListProtectionGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "shield:ListProtectionGroups", acct.ID, "", perr)
				return 0, 0, nil
			}
			if isShieldNotSubscribed(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("shield:ListProtectionGroups: %w", perr)
		}
		for _, g := range out.ProtectionGroups {
			arn := sv(g.ProtectionGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeShieldProtectionGroup,
				NativeID:       arn,
				Name:           g.ProtectionGroupId,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert shield protection groups: %w", uerr)
	}
	return len(batch), n, nil
}
