package aws

import (
	"context"
	"fmt"

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
	})
}

// shieldAPI is the narrow set of Shield operations called by the scanShield
// sub-phases.
type shieldAPI interface {
	DescribeSubscription(context.Context, *shield.DescribeSubscriptionInput, ...func(*shield.Options)) (*shield.DescribeSubscriptionOutput, error)
	ListProtections(context.Context, *shield.ListProtectionsInput, ...func(*shield.Options)) (*shield.ListProtectionsOutput, error)
	ListProtectionGroups(context.Context, *shield.ListProtectionGroupsInput, ...func(*shield.Options)) (*shield.ListProtectionGroupsOutput, error)
}

// scanShield discovers Shield Advanced subscription, protections, and
// protection groups. Shield is a global service; the client always uses
// us-east-1. Three phases run sequentially. Accounts without a Shield Advanced
// subscription return ResourceNotFoundException at every phase — tolerated as
// a no-op via isShieldNotSubscribed alongside the standard AccessDenied skip.
func scanShield(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := shield.NewFromConfig(acct.cfg, func(o *shield.Options) { o.Region = "us-east-1" })

	// Phase 1: subscription (singleton per account).
	if t, i, ferr := scanShieldSubscription(ctx, client, acct, st, scanID); ferr != nil {
		return 0, 0, ferr
	} else {
		total += t
		inserted += i
	}

	// Phase 2: protections.
	if t, i, ferr := scanShieldProtections(ctx, client, acct, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	// Phase 3: protection groups.
	if t, i, ferr := scanShieldProtectionGroups(ctx, client, acct, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
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

// shieldSubscriptionNativeID synthesises a stable identifier for the
// account-wide subscription. AWS sometimes populates Subscription.SubscriptionArn
// but not always; the synthetic form matches the canonical Shield ARN shape so
// rescans dedupe.
func shieldSubscriptionNativeID(accountID string) string {
	return fmt.Sprintf("arn:aws:shield::%s:subscription", accountID)
}

func scanShieldSubscription(ctx context.Context, client shieldAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	out, derr := client.DescribeSubscription(ctx, &shield.DescribeSubscriptionInput{})
	if derr != nil {
		if isAccessDenied(derr) {
			_ = skipIfAccessDenied(st, "shield:DescribeSubscription", acct.ID, "", derr)
			return 0, 0, nil
		}
		if isShieldNotSubscribed(derr) {
			return 0, 0, markServiceDisabled(derr)
		}
		return 0, 0, fmt.Errorf("shield:DescribeSubscription: %w", derr)
	}
	if out == nil || out.Subscription == nil {
		return 0, 0, nil
	}
	nid := sv(out.Subscription.SubscriptionArn)
	if nid == "" {
		nid = shieldSubscriptionNativeID(acct.ID)
	}
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeShieldSubscription,
		NativeID:       nid,
		Region:         nil,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert shield subscription: %w", uerr)
	}
	return 1, n, nil
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
