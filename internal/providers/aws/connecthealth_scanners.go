package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connecthealth"
	chtypes "github.com/aws/aws-sdk-go-v2/service/connecthealth/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeHealthAgentDomain, Service: "health-agent"})
	registerType(restype.Descriptor{Type: TypeHealthAgentSubscription, Service: "health-agent"})
	registerService(serviceEntry{
		name: "aws:health-agent",
		fn: func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
			client := connecthealth.NewFromConfig(acct.cfg, func(o *connecthealth.Options) { o.Region = region })
			return scanHealthAgent(ctx, client, acct, region, st, scanID)
		},
	})
}

// healthAgentAPI is the narrow Amazon Connect Health surface the scanners use.
type healthAgentAPI interface {
	connecthealth.ListDomainsAPIClient
	connecthealth.ListSubscriptionsAPIClient
	GetDomain(context.Context, *connecthealth.GetDomainInput, ...func(*connecthealth.Options)) (*connecthealth.GetDomainOutput, error)
}

// healthAgentPageCap is the documented maxResults cap of ListDomains and
// ListSubscriptions.
const healthAgentPageCap = 100

// scanHealthAgent discovers Amazon Connect Health domains and the
// subscriptions of each. Not scanned: patient insights jobs and medical scribe
// listening sessions (run records, readable only by id). The subscription
// phase fans out over the domains the first phase stored; a domain phase error
// leaves it no parents, so it is returned before that phase runs.
func scanHealthAgent(ctx context.Context, client healthAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	domains, total, inserted, err := scanHealthAgentDomains(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, i, err := scanHealthAgentSubscriptions(ctx, client, acct, region, st, scanID, domains)
	return total + t, inserted + i, err
}

// scanHealthAgentDomains lists the region's domains, then reads each with
// GetDomain: only the full domain carries its KMS key, web app configuration
// and tags. DELETED domains are not read, and a domain gone between the list
// and its Get is skipped. A denial mid-listing warns and keeps the domains
// listed so far.
func scanHealthAgentDomains(ctx context.Context, client healthAgentAPI, acct *account, region string, st *store.Store, scanID string) (healthAgentDomains, int, int, error) {
	var ids []string
	pager := connecthealth.NewListDomainsPaginator(client, &connecthealth.ListDomainsInput{},
		func(o *connecthealth.ListDomainsPaginatorOptions) {
			o.Limit = healthAgentPageCap
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "health-agent:ListDomains", acct.ID, region, err)
				break
			}
			return healthAgentDomains{}, 0, 0, fmt.Errorf("health-agent:ListDomains: %w", err)
		}
		for _, d := range out.Domains {
			if id := sv(d.DomainId); id != "" && d.Status != chtypes.DomainStatusDeleted {
				ids = append(ids, id)
			}
		}
	}
	var (
		mu       sync.Mutex
		idsByARN = map[string]string{}
	)
	rows, err := describeFanOut(ctx, st, acct, region, "health-agent:GetDomain", ids, isHealthAgentNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, err := client.GetDomain(ctx, &connecthealth.GetDomainInput{DomainId: &id})
			if err != nil {
				return nil, err
			}
			arn := sv(out.Arn)
			if arn == "" {
				return nil, nil
			}
			mu.Lock()
			idsByARN[arn] = id
			mu.Unlock()
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthAgentDomain, NativeID: arn, Name: out.Name, Region: &region,
				Status: nonEmptyPtr(string(out.Status)), CreatedAt: tp(out.CreatedAt),
				AttributesJSON: mustJSON(out), TagsJSON: mapTagsJSON(out.Tags),
				DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return healthAgentDomains{}, 0, 0, err
	}
	total, inserted, err := upsertBatch(st, rows, "health-agent domains")
	if err != nil {
		return healthAgentDomains{}, 0, 0, err
	}
	domains := healthAgentDomains{notActive: map[string]bool{}}
	for _, r := range rows {
		id := idsByARN[r.NativeID]
		domains.parents = append(domains.parents, childParent{id: id, arn: r.NativeID})
		if sv(r.Status) != string(chtypes.DomainStatusActive) {
			domains.notActive[id] = true
		}
	}
	return domains, total, inserted, nil
}

// healthAgentDomains is what the subscription phase needs from the domain
// phase: the stored domains, and the ids of those whose GetDomain status is
// not ACTIVE.
type healthAgentDomains struct {
	parents   []childParent
	notActive map[string]bool
}

// isHealthAgentNotFound matches a domain deleted between the call that listed
// it and a later call naming it.
func isHealthAgentNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanHealthAgentSubscriptions stores each domain's subscriptions (the billing
// and user-management records CreateSubscription makes) under the domain.
// ListSubscriptions returns the full subscription, so no Get is made. DELETED
// subscriptions are dropped; INACTIVE ones are kept. Per domain: one deleted
// after it was listed is skipped silently; one whose call still fails
// transiently after the SDK's retries is skipped with one warning. A domain
// that is not ACTIVE (being deleted) may refuse the listing with
// ValidationException (not observed live), so that code skips it with one
// warning; from an ACTIVE domain the code is returned, since the only inputs
// are the listed domain id and a fixed page size.
func scanHealthAgentSubscriptions(ctx context.Context, client healthAgentAPI, acct *account, region string, st *store.Store, scanID string, domains healthAgentDomains) (int, int, error) {
	const op = "health-agent:ListSubscriptions"
	var notReady skipTally
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, op, domains.parents, isHealthAgentNotFound, byParentID,
		func(ctx context.Context, domain childParent) ([]*store.Resource, error) {
			rows, err := listHealthAgentSubscriptions(ctx, client, acct, region, scanID, domain)
			if domains.notActive[domain.id] && notReady.dropCode("ValidationException", domain.id, err) {
				return nil, nil
			}
			return rows, err
		})
	notReady.warnCode(st, op, acct, region, "ValidationException", "domain(s)",
		"a domain that is not ACTIVE may refuse its subscription listing")
	return total, inserted, err
}

// listHealthAgentSubscriptions returns the subscriptions of one domain.
func listHealthAgentSubscriptions(ctx context.Context, client healthAgentAPI, acct *account, region, scanID string, domain childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := connecthealth.NewListSubscriptionsPaginator(client, &connecthealth.ListSubscriptionsInput{DomainId: &domain.id},
		func(o *connecthealth.ListSubscriptionsPaginatorOptions) {
			o.Limit = healthAgentPageCap
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range out.Subscriptions {
			arn := sv(s.Arn)
			if arn == "" || s.Status == chtypes.SubscriptionStatusDeleted {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthAgentSubscription, NativeID: arn, Name: s.SubscriptionId, Region: &region,
				Status: nonEmptyPtr(string(s.Status)), CreatedAt: tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
