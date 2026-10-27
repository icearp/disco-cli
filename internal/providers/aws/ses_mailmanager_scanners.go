package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mailmanager"
	mmtypes "github.com/aws/aws-sdk-go-v2/service/mailmanager/types"
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
	ListTagsForResource(context.Context, *mailmanager.ListTagsForResourceInput, ...func(*mailmanager.Options)) (*mailmanager.ListTagsForResourceOutput, error)
}

const (
	mmListTagsOp          = "mailmanager:ListTagsForResource"
	mmListArchivesOp      = "mailmanager:ListArchives"
	mmListIngressPointsOp = "mailmanager:ListIngressPoints"
)

// mmNotFound is the only not-found code ListTagsForResource, GetArchive and
// GetIngressPoint model.
var mmNotFound = []string{"ResourceNotFoundException"}

// mmPageCap is the PageSize cap (1-50) the mailmanager model documents for
// every listing but ListRelays, which documents none and is sent none.
const mmPageCap int32 = 50

// mmARN builds a Mail Manager resource's ARN in the Service Reference's
// mailmanager-{kind}/{id} format, for the types whose list element carries
// no ARN.
func mmARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "ses", region, acct, "mailmanager-"+kind+"/"+id)
}

// mailManagerTags reads a row's tags with mailmanager's ListTagsForResource,
// sent the row's NativeID. The op's TaggableResourceArn pattern accepts the
// mailmanager-* and addon-* ARNs of every Mail Manager row in the aws,
// aws-cn, aws-us-gov and aws-eusc partitions only; the SDK does not check
// the pattern before sending. The op answers with every tag at once (no
// page token).
func mailManagerTags(client mailManagerAPI) rowTagReader {
	return rowTagReader{
		op:       mmListTagsOp,
		notFound: mmNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &mailmanager.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t mmtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// mmIDRow is the row of a listed Mail Manager item without an ARN, stored
// under the ARN of kind built from id and named name, or id when it has
// none; an item without an id is not stored, and what names it in the
// warning.
func mmIDRow(acct *account, region, scanID, typ, kind, what, id, name string, item any) (*store.Resource, string, string) {
	if id == "" {
		return nil, what + " have an empty id", cmp.Or(name, "(an item with no name)")
	}
	return bacRowAt(acct, region, scanID, typ, mmARN(acct.partition(), region, acct.ID, kind, id), cmp.Or(name, id), item), "", ""
}

func scanMMAddonInstances(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "mailmanager:ListAddonInstances",
		func(ctx context.Context, token *string) ([]mmtypes.AddonInstance, *string, error) {
			out, err := client.ListAddonInstances(ctx, &mailmanager.ListAddonInstancesInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AddonInstances, out.NextToken, nil
		},
		func(a mmtypes.AddonInstance) (*store.Resource, string, string) {
			name := cmp.Or(sv(a.AddonName), sv(a.AddonInstanceId))
			return bacRow(acct, region, scanID, TypeSESMailManagerAddonInstance, "add-on instances", a.AddonInstanceArn, name, cmp.Or(name, "(an instance with no name)"), a)
		}, tags, p)
}

func scanMMAddonSubscriptions(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "mailmanager:ListAddonSubscriptions",
		func(ctx context.Context, token *string) ([]mmtypes.AddonSubscription, *string, error) {
			out, err := client.ListAddonSubscriptions(ctx, &mailmanager.ListAddonSubscriptionsInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AddonSubscriptions, out.NextToken, nil
		},
		func(a mmtypes.AddonSubscription) (*store.Resource, string, string) {
			name := cmp.Or(sv(a.AddonName), sv(a.AddonSubscriptionId))
			return bacRow(acct, region, scanID, TypeSESMailManagerAddonSubscription, "add-on subscriptions", a.AddonSubscriptionArn, name, cmp.Or(name, "(a subscription with no name)"), a)
		}, tags, p)
}

// scanMMAddressLists stores the region's address lists with their tags.
// The tag read follows the rule scanSESMultiRegionEndpoints states: the tag
// op's TaggableResourceArn pattern accepts the element's
// mailmanager-address-list ARN, and the Service Reference lists
// mailmanager-address-list under TagResource (though not under
// ListTagsForResource).
func scanMMAddressLists(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "mailmanager:ListAddressLists",
		func(ctx context.Context, token *string) ([]mmtypes.AddressList, *string, error) {
			out, err := client.ListAddressLists(ctx, &mailmanager.ListAddressListsInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AddressLists, out.NextToken, nil
		},
		func(a mmtypes.AddressList) (*store.Resource, string, string) {
			name := cmp.Or(sv(a.AddressListName), sv(a.AddressListId))
			return bacRow(acct, region, scanID, TypeSESMailManagerAddressList, "address lists", a.AddressListArn, name, cmp.Or(name, "(an address list with no name)"), a)
		}, tags, p)
}

// scanMMDetailed stores a Mail Manager listing whose rows are replaced by
// their Get body (readRowDetails) and then tag-read: get reads the body of
// the resource whose id the row builder recorded in ids.
func scanMMDetailed[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, getOp, what string, tags rowTagReader, p *phaseTotals,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item, map[*store.Resource]string) (*store.Resource, string, string),
	get func(ctx context.Context, id string) (any, error),
) (storeWriteFailed bool) {
	ids := map[*store.Resource]string{}
	d := &detailedRows{getOp: getOp, what: what, notFound: mmNotFound, tags: tags}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op, page,
		func(it Item) (*store.Resource, string, string) { return row(it, ids) },
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			return get(ctx, ids[r])
		}))
	return d.addPhase(p, op, total, inserted, err)
}

// scanMMArchives stores each archive as its GetArchive body, which adds the
// KmsKeyArn the list element lacks.
func scanMMArchives(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return scanMMDetailed(ctx, st, acct, region, mmListArchivesOp, "mailmanager:GetArchive", "archives", tags, p,
		func(ctx context.Context, token *string) ([]mmtypes.Archive, *string, error) {
			out, err := client.ListArchives(ctx, &mailmanager.ListArchivesInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Archives, out.NextToken, nil
		},
		func(a mmtypes.Archive, ids map[*store.Resource]string) (*store.Resource, string, string) {
			r, why, key := mmIDRow(acct, region, scanID, TypeSESMailManagerArchive, "archive", "archives", sv(a.ArchiveId), sv(a.ArchiveName), a)
			if r != nil {
				ids[r] = sv(a.ArchiveId)
			}
			return r, why, key
		},
		func(ctx context.Context, id string) (any, error) {
			return client.GetArchive(ctx, &mailmanager.GetArchiveInput{ArchiveId: &id})
		})
}

// scanMMIngressPoints stores each ingress point as its GetIngressPoint body,
// which adds the RuleSetId and TrafficPolicyId the list element lacks.
func scanMMIngressPoints(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return scanMMDetailed(ctx, st, acct, region, mmListIngressPointsOp, "mailmanager:GetIngressPoint", "ingress points", tags, p,
		func(ctx context.Context, token *string) ([]mmtypes.IngressPoint, *string, error) {
			out, err := client.ListIngressPoints(ctx, &mailmanager.ListIngressPointsInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IngressPoints, out.NextToken, nil
		},
		func(ip mmtypes.IngressPoint, ids map[*store.Resource]string) (*store.Resource, string, string) {
			r, why, key := mmIDRow(acct, region, scanID, TypeSESMailManagerIngressPoint, "ingress-point", "ingress points", sv(ip.IngressPointId), sv(ip.IngressPointName), ip)
			if r != nil {
				ids[r] = sv(ip.IngressPointId)
			}
			return r, why, key
		},
		func(ctx context.Context, id string) (any, error) {
			return client.GetIngressPoint(ctx, &mailmanager.GetIngressPointInput{IngressPointId: &id})
		})
}

// scanMMRelays stores the region's SMTP relays under the Service
// Reference's mailmanager-smtp-relay ARN format, the form the tag read needs.
func scanMMRelays(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "mailmanager:ListRelays",
		func(ctx context.Context, token *string) ([]mmtypes.Relay, *string, error) {
			out, err := client.ListRelays(ctx, &mailmanager.ListRelaysInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Relays, out.NextToken, nil
		},
		func(r mmtypes.Relay) (*store.Resource, string, string) {
			return mmIDRow(acct, region, scanID, TypeSESMailManagerRelay, "smtp-relay", "relays", sv(r.RelayId), sv(r.RelayName), r)
		}, tags, p)
}

func scanMMRuleSets(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "mailmanager:ListRuleSets",
		func(ctx context.Context, token *string) ([]mmtypes.RuleSet, *string, error) {
			out, err := client.ListRuleSets(ctx, &mailmanager.ListRuleSetsInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RuleSets, out.NextToken, nil
		},
		func(r mmtypes.RuleSet) (*store.Resource, string, string) {
			return mmIDRow(acct, region, scanID, TypeSESMailManagerRuleSet, "rule-set", "rule sets", sv(r.RuleSetId), sv(r.RuleSetName), r)
		}, tags, p)
}

func scanMMTrafficPolicies(ctx context.Context, client mailManagerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "mailmanager:ListTrafficPolicies",
		func(ctx context.Context, token *string) ([]mmtypes.TrafficPolicy, *string, error) {
			out, err := client.ListTrafficPolicies(ctx, &mailmanager.ListTrafficPoliciesInput{NextToken: token, PageSize: sdkaws.Int32(mmPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TrafficPolicies, out.NextToken, nil
		},
		func(t mmtypes.TrafficPolicy) (*store.Resource, string, string) {
			return mmIDRow(acct, region, scanID, TypeSESMailManagerTrafficPolicy, "traffic-policy", "traffic policies", sv(t.TrafficPolicyId), sv(t.TrafficPolicyName), t)
		}, tags, p)
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
