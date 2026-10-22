package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/wickr"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWickrNetwork, Service: "wickr"})
	registerType(restype.Descriptor{Type: TypeWickrSecurityGroup, Service: "wickr"})
	registerType(restype.Descriptor{Type: TypeWickrBot, Service: "wickr"})
	// A network's settings come into being with the network and have no delete
	// op (UpdateNetworkSettings only changes them). Still not Managed: the
	// customer sets them, and Managed rows are left out of default `disco check`
	// input.
	registerType(restype.Descriptor{Type: TypeWickrNetworkSetting, Service: "wickr", Untaggable: true})
	// InviteCode is the registration code with which its holder joins the
	// network as that user.
	registerType(restype.Descriptor{
		Type: TypeWickrUser, Service: "wickr",
		Redact: []redact.Rule{{Path: "InviteCode", Mode: redact.RedactScalar}},
	})
	registerService(serviceEntry{name: "aws:wickr", fn: scanWickr})
}

type wickrAPI interface {
	ListNetworks(context.Context, *wickr.ListNetworksInput, ...func(*wickr.Options)) (*wickr.ListNetworksOutput, error)
	ListSecurityGroups(context.Context, *wickr.ListSecurityGroupsInput, ...func(*wickr.Options)) (*wickr.ListSecurityGroupsOutput, error)
	ListBots(context.Context, *wickr.ListBotsInput, ...func(*wickr.Options)) (*wickr.ListBotsOutput, error)
	GetNetworkSettings(context.Context, *wickr.GetNetworkSettingsInput, ...func(*wickr.Options)) (*wickr.GetNetworkSettingsOutput, error)
	ListUsers(context.Context, *wickr.ListUsersInput, ...func(*wickr.Options)) (*wickr.ListUsersOutput, error)
}

const (
	wickrListNetworksOp       = "wickr:ListNetworks"
	wickrListSecurityGroupsOp = "wickr:ListSecurityGroups"
	wickrListBotsOp           = "wickr:ListBots"
	wickrGetNetworkSettingsOp = "wickr:GetNetworkSettings"
	wickrListUsersOp          = "wickr:ListUsers"
)

// wickrPageLimit is the maxResults cap the model documents for every Wickr
// list op disco calls.
const wickrPageLimit = 100

// wickrRateLimitCode is Wickr's modeled throttle (HTTP 429). The SDK retryer
// recognises neither the code nor the status.
const wickrRateLimitCode = "RateLimitError"

// wickrNetworkSkipCodes are the per-network refusals a child phase skips,
// keeping the other networks' rows, with one warning per code.
var wickrNetworkSkipCodes = []skipCode{
	{code: "ForbiddenError", why: "Wickr answers a caller lacking permission on the network with it"},
	{code: wickrRateLimitCode, why: "Wickr still throttled the network after the SDK's retries"},
}

// scanWickr discovers AWS Wickr networks with their security groups, bots,
// network settings and users.
func scanWickr(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := wickr.NewFromConfig(acct.cfg, wickrClientOptions(region))
	return scanWickrAll(ctx, client, acct, region, st, scanID)
}

// wickrClientOptions pins the region and retries RateLimitError, which would
// otherwise fail a throttled call on its first attempt. The code is retried
// with the retryer's standard backoff only: AddWithErrorCodes changes which
// errors are retryable, not which count as throttles, so adaptive mode's
// client-side rate limiting does not learn from it.
func wickrClientOptions(region string) func(*wickr.Options) {
	return func(o *wickr.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, wickrRateLimitCode)
	}
}

// scanWickrAll runs the network phase, then the security group, bot, network
// settings and user phases over the networks it stored. A failed network phase
// stores nothing and lists no parents, so it returns alone. The child phases
// are independent: each runs whatever the others returned, and their errors
// are joined. Each tallies its transient errors per network, so no child
// phase's error is transient and none needs demoteTransient.
func scanWickrAll(ctx context.Context, client wickrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, networks, err := scanWickrNetworks(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	var errs []error
	for _, phase := range []func(context.Context, wickrAPI, *account, string, *store.Store, string, []childParent) (int, int, error){
		scanWickrSecurityGroups, scanWickrBots, scanWickrNetworkSettings, scanWickrUsers,
	} {
		t, i, err := phase(ctx, client, acct, region, st, scanID, networks)
		total, inserted = total+t, inserted+i
		errs = append(errs, err)
	}
	return total, inserted, errors.Join(errs...)
}

// isWickrDenied reports an IAM denial: an AWS access-denied code, or
// ForbiddenError, Wickr's modeled 403 for a caller lacking permission.
func isWickrDenied(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, "ForbiddenError")
}

// scanWickrNetworks stores the region's networks, NativeID = NetworkArn, and
// returns the stored networks that carry a NetworkId as the parents of the
// child phases.
func scanWickrNetworks(ctx context.Context, client wickrAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, networks []childParent, err error) {
	pager := wickr.NewListNetworksPaginator(client, &wickr.ListNetworksInput{}, func(o *wickr.ListNetworksPaginatorOptions) {
		o.Limit = wickrPageLimit
		o.StopOnDuplicateToken = true
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isWickrDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, wickrListNetworksOp, acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("%s: %w", wickrListNetworksOp, err)
		}
		for _, n := range out.Networks {
			arn := sv(n.NetworkArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWickrNetwork, NativeID: arn, Name: n.NetworkName, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
			if id := sv(n.NetworkId); id != "" {
				networks = append(networks, childParent{id: id, arn: arn})
			}
		}
	}
	total, inserted, err = upsertBatch(st, batch, "wickr networks")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, networks, nil
}

// wickrChildNativeID keys a network child under the network ARN: the API
// returns no child ARN and the Service Reference defines only the network
// ARN. Security group and bot ids have no model pattern, so the id goes last.
func wickrChildNativeID(networkARN, kind, id string) string {
	return networkARN + "/" + kind + "/" + id
}

// wickrChildFanOut runs storeChildren for each network. Per network: a network
// deleted since listing (ResourceNotFoundError) is skipped silently, an
// AccessDenied-coded denial warns once, ForbiddenError, RateLimitError and a
// transient error after the SDK's retries each skip the network with one
// warning for all such networks, and anything else fails the phase; rows a
// network stored before its error stay.
func wickrChildFanOut(ctx context.Context, st *store.Store, acct *account, region, op string, networks []childParent,
	storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundError") }
	return childFanOutStoredSkipCodes(ctx, st, acct, region, op, networks, isGone, byParentARN, "network(s)", wickrNetworkSkipCodes, storeChildren)
}

// wickrStorePages pages one network's child listing and stores each page as it
// arrives, linked under the network, so a network with many users holds one
// page in memory.
func wickrStorePages[Page any](ctx context.Context, st *store.Store, op, networkID string,
	hasMore func() bool, next func(context.Context) (Page, error), rows func(Page) []*store.Resource,
) (int, int, error) {
	var total, inserted int
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			return total, inserted, err
		}
		var children []containedChild
		for _, r := range rows(page) {
			children = append(children, containedChild{row: r, parentID: networkID})
		}
		t, i, err := upsertContained(st, op, children)
		total, inserted = total+t, inserted+i
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

// scanWickrSecurityGroups stores each network's security groups. The default
// group (IsDefault) is not provider-managed although DeleteSecurityGroup rejects
// it: UpdateSecurityGroup changes its settings, which are customer posture.
func scanWickrSecurityGroups(ctx context.Context, client wickrAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent) (int, int, error) {
	return wickrChildFanOut(ctx, st, acct, region, wickrListSecurityGroupsOp, networks,
		func(ctx context.Context, n childParent) (int, int, error) {
			pager := wickr.NewListSecurityGroupsPaginator(client, &wickr.ListSecurityGroupsInput{NetworkId: sdkaws.String(n.id)},
				func(o *wickr.ListSecurityGroupsPaginatorOptions) {
					o.Limit = wickrPageLimit
					o.StopOnDuplicateToken = true
				})
			next := func(ctx context.Context) (*wickr.ListSecurityGroupsOutput, error) { return pager.NextPage(ctx) }
			return wickrStorePages(ctx, st, wickrListSecurityGroupsOp, store.ResourceID("aws", acct.ID, n.arn), pager.HasMorePages, next,
				func(out *wickr.ListSecurityGroupsOutput) []*store.Resource {
					var rows []*store.Resource
					for _, g := range out.SecurityGroups {
						id := sv(g.Id)
						if id == "" {
							continue
						}
						rows = append(rows, &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeWickrSecurityGroup, NativeID: wickrChildNativeID(n.arn, "security-group", id),
							Name: g.Name, Region: &region,
							AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
						})
					}
					return rows
				})
		})
}

// scanWickrBots stores each network's bots.
func scanWickrBots(ctx context.Context, client wickrAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent) (int, int, error) {
	return wickrChildFanOut(ctx, st, acct, region, wickrListBotsOp, networks,
		func(ctx context.Context, n childParent) (int, int, error) {
			pager := wickr.NewListBotsPaginator(client, &wickr.ListBotsInput{NetworkId: sdkaws.String(n.id)},
				func(o *wickr.ListBotsPaginatorOptions) {
					o.Limit = wickrPageLimit
					o.StopOnDuplicateToken = true
				})
			next := func(ctx context.Context) (*wickr.ListBotsOutput, error) { return pager.NextPage(ctx) }
			return wickrStorePages(ctx, st, wickrListBotsOp, store.ResourceID("aws", acct.ID, n.arn), pager.HasMorePages, next,
				func(out *wickr.ListBotsOutput) []*store.Resource {
					var rows []*store.Resource
					for _, b := range out.Bots {
						id := sv(b.BotId)
						if id == "" {
							continue
						}
						rows = append(rows, &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeWickrBot, NativeID: wickrChildNativeID(n.arn, "bot", id),
							Name: b.Username, Region: &region,
							AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
						})
					}
					return rows
				})
		})
}

// wickrNetworkSettingsNativeID keys a network's settings, a singleton of the
// network, under the network ARN.
func wickrNetworkSettingsNativeID(networkARN string) string {
	return networkARN + "/network-settings"
}

// scanWickrNetworkSettings stores the settings of each network that returns
// any, one row per network holding the whole GetNetworkSettings body.
func scanWickrNetworkSettings(ctx context.Context, client wickrAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent) (int, int, error) {
	return wickrChildFanOut(ctx, st, acct, region, wickrGetNetworkSettingsOp, networks,
		func(ctx context.Context, n childParent) (int, int, error) {
			out, err := client.GetNetworkSettings(ctx, &wickr.GetNetworkSettingsInput{NetworkId: sdkaws.String(n.id)})
			if err != nil || len(out.Settings) == 0 {
				return 0, 0, err
			}
			return upsertContained(st, wickrGetNetworkSettingsOp, []containedChild{{
				parentID: store.ResourceID("aws", acct.ID, n.arn),
				row: &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWickrNetworkSetting, NativeID: wickrNetworkSettingsNativeID(n.arn), Region: &region,
					AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
				},
			}})
		})
}

// scanWickrUsers stores each network's users a page at a time, since a
// network's user count has no small bound.
func scanWickrUsers(ctx context.Context, client wickrAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent) (int, int, error) {
	return wickrChildFanOut(ctx, st, acct, region, wickrListUsersOp, networks,
		func(ctx context.Context, n childParent) (int, int, error) {
			pager := wickr.NewListUsersPaginator(client, &wickr.ListUsersInput{NetworkId: sdkaws.String(n.id)},
				func(o *wickr.ListUsersPaginatorOptions) {
					o.Limit = wickrPageLimit
					o.StopOnDuplicateToken = true
				})
			next := func(ctx context.Context) (*wickr.ListUsersOutput, error) { return pager.NextPage(ctx) }
			return wickrStorePages(ctx, st, wickrListUsersOp, store.ResourceID("aws", acct.ID, n.arn), pager.HasMorePages, next,
				func(out *wickr.ListUsersOutput) []*store.Resource {
					var rows []*store.Resource
					for _, u := range out.Users {
						id := sv(u.UserId)
						// The model's User shape covers bots too (IsUser is "as
						// opposed to a bot"; a bot's username must end in "bot"),
						// so ListUsers may return the bots ListBots stores; that
						// is unverified. Only an explicit false is skipped.
						if id == "" || (u.IsUser != nil && !*u.IsUser) {
							continue
						}
						rows = append(rows, &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeWickrUser, NativeID: wickrChildNativeID(n.arn, "user", id),
							Name: u.Username, Region: &region,
							AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
						})
					}
					return rows
				})
		})
}
