package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/mailmanager"
	"github.com/icearp/disco-cli/store"
)

// mailManagerAPI — narrow set of MailManager ops. MailManager is a
// distinct SDK module (aws-sdk-go-v2/service/mailmanager) for SES Mail
// Manager's rule-driven inbound mail processing.
type mailManagerAPI interface {
	ListAddonInstances(context.Context, *mailmanager.ListAddonInstancesInput, ...func(*mailmanager.Options)) (*mailmanager.ListAddonInstancesOutput, error)
	ListAddonSubscriptions(context.Context, *mailmanager.ListAddonSubscriptionsInput, ...func(*mailmanager.Options)) (*mailmanager.ListAddonSubscriptionsOutput, error)
	ListAddressLists(context.Context, *mailmanager.ListAddressListsInput, ...func(*mailmanager.Options)) (*mailmanager.ListAddressListsOutput, error)
	ListArchives(context.Context, *mailmanager.ListArchivesInput, ...func(*mailmanager.Options)) (*mailmanager.ListArchivesOutput, error)
	ListIngressPoints(context.Context, *mailmanager.ListIngressPointsInput, ...func(*mailmanager.Options)) (*mailmanager.ListIngressPointsOutput, error)
	ListRelays(context.Context, *mailmanager.ListRelaysInput, ...func(*mailmanager.Options)) (*mailmanager.ListRelaysOutput, error)
	ListRuleSets(context.Context, *mailmanager.ListRuleSetsInput, ...func(*mailmanager.Options)) (*mailmanager.ListRuleSetsOutput, error)
	ListTrafficPolicies(context.Context, *mailmanager.ListTrafficPoliciesInput, ...func(*mailmanager.Options)) (*mailmanager.ListTrafficPoliciesOutput, error)
	GetIngressPoint(context.Context, *mailmanager.GetIngressPointInput, ...func(*mailmanager.Options)) (*mailmanager.GetIngressPointOutput, error)
	GetArchive(context.Context, *mailmanager.GetArchiveInput, ...func(*mailmanager.Options)) (*mailmanager.GetArchiveOutput, error)
}

func scanSESMailManager(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanMMAddonInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMAddonSubscriptions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMAddressLists(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMArchives(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMIngressPoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMRelays(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMRuleSets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMMTrafficPolicies(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func mmARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:ses:%s:%s:mailmanager-%s/%s", region, acct, kind, id)
}

func scanMMAddonInstances(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListAddonInstancesPaginator(client, &mailmanager.ListAddonInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListAddonInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListAddonInstances: %w", perr)
		}
		for _, a := range out.AddonInstances {
			arn := sv(a.AddonInstanceArn)
			if arn == "" {
				continue
			}
			label := sv(a.AddonName)
			if label == "" {
				label = sv(a.AddonInstanceId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerAddonInstance, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "addon-instances")
}

func scanMMAddonSubscriptions(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListAddonSubscriptionsPaginator(client, &mailmanager.ListAddonSubscriptionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListAddonSubscriptions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListAddonSubscriptions: %w", perr)
		}
		for _, a := range out.AddonSubscriptions {
			arn := sv(a.AddonSubscriptionArn)
			if arn == "" {
				continue
			}
			label := sv(a.AddonName)
			if label == "" {
				label = sv(a.AddonSubscriptionId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerAddonSubscription, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "addon-subscriptions")
}

func scanMMAddressLists(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListAddressListsPaginator(client, &mailmanager.ListAddressListsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListAddressLists", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListAddressLists: %w", perr)
		}
		for _, a := range out.AddressLists {
			arn := sv(a.AddressListArn)
			if arn == "" {
				continue
			}
			label := sv(a.AddressListName)
			if label == "" {
				label = sv(a.AddressListId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerAddressList, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "address-lists")
}

func scanMMArchives(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListArchivesPaginator(client, &mailmanager.ListArchivesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListArchives", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListArchives: %w", perr)
		}
		for _, a := range out.Archives {
			id := sv(a.ArchiveId)
			if id == "" {
				continue
			}
			label := sv(a.ArchiveName)
			if label == "" {
				label = id
			}
			// Enrich each archive via GetArchive — adds KmsKeyArn ref.
			detail, derr := client.GetArchive(ctx, &mailmanager.GetArchiveInput{ArchiveId: &id})
			attrs := mustJSON(a)
			if derr == nil && detail != nil {
				attrs = mustJSON(detail)
			} else if derr != nil && isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "mailmanager:GetArchive", acct.ID, region, derr)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerArchive, NativeID: mmARN(region, acct.ID, "archive", id),
				Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "archives")
}

func scanMMIngressPoints(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListIngressPointsPaginator(client, &mailmanager.ListIngressPointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListIngressPoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListIngressPoints: %w", perr)
		}
		for _, p := range out.IngressPoints {
			id := sv(p.IngressPointId)
			if id == "" {
				continue
			}
			label := sv(p.IngressPointName)
			if label == "" {
				label = id
			}
			// Enrich via GetIngressPoint — adds RuleSetId, TrafficPolicyId
			// cross-refs not present in summary.
			detail, derr := client.GetIngressPoint(ctx, &mailmanager.GetIngressPointInput{IngressPointId: &id})
			attrs := mustJSON(p)
			if derr == nil && detail != nil {
				attrs = mustJSON(detail)
			} else if derr != nil && isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "mailmanager:GetIngressPoint", acct.ID, region, derr)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerIngressPoint, NativeID: mmARN(region, acct.ID, "ingress-point", id),
				Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "ingress-points")
}

func scanMMRelays(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListRelaysPaginator(client, &mailmanager.ListRelaysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListRelays", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListRelays: %w", perr)
		}
		for _, r := range out.Relays {
			id := sv(r.RelayId)
			if id == "" {
				continue
			}
			label := sv(r.RelayName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerRelay, NativeID: mmARN(region, acct.ID, "relay", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "relays")
}

func scanMMRuleSets(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListRuleSetsPaginator(client, &mailmanager.ListRuleSetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListRuleSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListRuleSets: %w", perr)
		}
		for _, r := range out.RuleSets {
			id := sv(r.RuleSetId)
			if id == "" {
				continue
			}
			label := sv(r.RuleSetName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerRuleSet, NativeID: mmARN(region, acct.ID, "rule-set", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "rule-sets")
}

func scanMMTrafficPolicies(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mailmanager.NewListTrafficPoliciesPaginator(client, &mailmanager.ListTrafficPoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mailmanager:ListTrafficPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mailmanager:ListTrafficPolicies: %w", perr)
		}
		for _, t := range out.TrafficPolicies {
			id := sv(t.TrafficPolicyId)
			if id == "" {
				continue
			}
			label := sv(t.TrafficPolicyName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMailManagerTrafficPolicy, NativeID: mmARN(region, acct.ID, "traffic-policy", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertSESMM(st, batch, "traffic-policies")
}

func upsertSESMM(st *store.Store, batch []*store.Resource, kind string) (int, int, error) {
	return upsertBatch(st, batch, "ses mailmanager-"+kind)
}

// upsertBatch is a generic helper for scanners that fan out into a batch and
// need uniform empty-skip + error formatting. label names the resource
// family for error messages.
func upsertBatch(st *store.Store, batch []*store.Resource, label string) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, uerr)
	}
	return len(batch), n, nil
}
