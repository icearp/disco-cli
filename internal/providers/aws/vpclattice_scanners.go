package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/vpclattice"
	"github.com/aws/aws-sdk-go-v2/service/vpclattice/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeVpcLatticeService, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetwork, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeListener, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeRule, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeTargetGroup, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeAccessLogSubscription, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeAuthPolicy, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourcePolicy, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeDomainVerification, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourceConfiguration, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourceGateway, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkResourceAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkServiceAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkVpcAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourceEndpointAssociation, Service: "vpclattice"})
	registerService(serviceEntry{
		name: "aws:vpclattice",
		fn:   scanVpcLattice,
	})
}

type vpcLatticeAPI interface {
	ListServices(context.Context, *vpclattice.ListServicesInput, ...func(*vpclattice.Options)) (*vpclattice.ListServicesOutput, error)
	GetService(context.Context, *vpclattice.GetServiceInput, ...func(*vpclattice.Options)) (*vpclattice.GetServiceOutput, error)
	ListServiceNetworks(context.Context, *vpclattice.ListServiceNetworksInput, ...func(*vpclattice.Options)) (*vpclattice.ListServiceNetworksOutput, error)
	ListListeners(context.Context, *vpclattice.ListListenersInput, ...func(*vpclattice.Options)) (*vpclattice.ListListenersOutput, error)
	ListRules(context.Context, *vpclattice.ListRulesInput, ...func(*vpclattice.Options)) (*vpclattice.ListRulesOutput, error)
	ListTargetGroups(context.Context, *vpclattice.ListTargetGroupsInput, ...func(*vpclattice.Options)) (*vpclattice.ListTargetGroupsOutput, error)
	ListAccessLogSubscriptions(context.Context, *vpclattice.ListAccessLogSubscriptionsInput, ...func(*vpclattice.Options)) (*vpclattice.ListAccessLogSubscriptionsOutput, error)
	GetAuthPolicy(context.Context, *vpclattice.GetAuthPolicyInput, ...func(*vpclattice.Options)) (*vpclattice.GetAuthPolicyOutput, error)
	GetResourcePolicy(context.Context, *vpclattice.GetResourcePolicyInput, ...func(*vpclattice.Options)) (*vpclattice.GetResourcePolicyOutput, error)
	ListDomainVerifications(context.Context, *vpclattice.ListDomainVerificationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListDomainVerificationsOutput, error)
	ListResourceConfigurations(context.Context, *vpclattice.ListResourceConfigurationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListResourceConfigurationsOutput, error)
	ListResourceGateways(context.Context, *vpclattice.ListResourceGatewaysInput, ...func(*vpclattice.Options)) (*vpclattice.ListResourceGatewaysOutput, error)
	ListServiceNetworkResourceAssociations(context.Context, *vpclattice.ListServiceNetworkResourceAssociationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListServiceNetworkResourceAssociationsOutput, error)
	ListServiceNetworkServiceAssociations(context.Context, *vpclattice.ListServiceNetworkServiceAssociationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListServiceNetworkServiceAssociationsOutput, error)
	ListServiceNetworkVpcAssociations(context.Context, *vpclattice.ListServiceNetworkVpcAssociationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListServiceNetworkVpcAssociationsOutput, error)
	ListResourceEndpointAssociations(context.Context, *vpclattice.ListResourceEndpointAssociationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListResourceEndpointAssociationsOutput, error)
	ListServiceNetworkVpcEndpointAssociations(context.Context, *vpclattice.ListServiceNetworkVpcEndpointAssociationsInput, ...func(*vpclattice.Options)) (*vpclattice.ListServiceNetworkVpcEndpointAssociationsOutput, error)
	ListTargets(context.Context, *vpclattice.ListTargetsInput, ...func(*vpclattice.Options)) (*vpclattice.ListTargetsOutput, error)
	ListTagsForResource(context.Context, *vpclattice.ListTagsForResourceInput, ...func(*vpclattice.Options)) (*vpclattice.ListTagsForResourceOutput, error)
}

const (
	// vlPageCap is the model's maxResults cap (1 to 100) of every VPC Lattice
	// listing scanned.
	vlPageCap = 100

	vlListTagsOp = "vpclattice:ListTagsForResource"
)

// vlNotFound is the only not-found code modeled by ListTagsForResource,
// GetService, ListListeners and ListRules. The other child listings model
// none, so a parent deleted after it was listed fails its listing.
var vlNotFound = []string{"ResourceNotFoundException"}

// vlRow is the row of a listed VPC Lattice item stored under its ARN, item
// verbatim as the attributes.
func vlRow(acct *account, region, scanID, typ, arn, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// vlParentPages is page with a denial for another account's parent (one
// shared in through RAM: otherAccountGap) ending its listing silently,
// keeping the pages read.
func vlParentPages[Item any](acctID string, page func(ctx context.Context, parent string, token *string) ([]Item, *string, error)) func(ctx context.Context, parent string, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, parent string, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, parent, token)
		if otherAccountGap(parent, acctID, err, nil) {
			return nil, nil, nil
		}
		return items, next, err
	}
}

// vlTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries. The Service Reference lists
// TagResource for every type read through it. The op answers with every tag
// at once (no page token). A resource another account owns (one shared in
// through RAM, or a child of one) is read under otherAccountGap.
func vlTags(client vpcLatticeAPI, acctID string) rowTagReader {
	return rowTagReader{
		op:       vlListTagsOp,
		notFound: vlNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &vpclattice.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if otherAccountGap(r.NativeID, acctID, err, vlNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

func scanVpcLattice(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := vpclattice.NewFromConfig(acct.cfg, func(o *vpclattice.Options) { o.Region = region })
	return runHostPhases(ctx, st, acct, region, "vpclattice", 0, vlPhases(ctx, client, acct, region, st, scanID))
}

// vlPhases is the vpclattice scan's phases in the order they run
// (runHostPhases): every phase runs whatever an earlier one returned, and a
// child phase gets the parents its parent phase stored. Listeners, access
// log subscriptions and the auth and resource policies read the stored
// services; access log subscriptions, the policies and the service and VPC
// associations the stored service networks; rules the stored listeners;
// resource endpoint associations the stored resource configurations. The
// VPC endpoint association and target phases run last. The phases run one
// at a time, so readRowTags's own bound is the bound on the tag reads in
// flight. Every op is on one host.
func vlPhases(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) []hostPhase {
	var (
		tags                     = vlTags(client, acct.ID)
		services, networks       []childParent
		listeners, configs, tgs  []childParent
		serviceOfListener        = map[string]string{}
		servicesAndNetworks      = func() []childParent { return append(append([]childParent{}, services...), networks...) }
		serviceARNs, networkARNs = func() []string { return childParentIDs(services) }, func() []string { return childParentIDs(networks) }
	)
	return []hostPhase{
		{"vpclattice:ListServices", 0, func(p *phaseTotals) (stop bool) {
			services, stop = scanVLServices(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		}},
		{"vpclattice:ListServiceNetworks", 0, func(p *phaseTotals) (stop bool) {
			networks, stop = scanVLServiceNetworks(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		}},
		{"vpclattice:ListListeners", 0, func(p *phaseTotals) (stop bool) {
			listeners, stop = scanVLListeners(ctx, client, acct, region, st, scanID, services, serviceOfListener, tags, p)
			return stop
		}},
		{"vpclattice:ListResourceConfigurations", 0, func(p *phaseTotals) (stop bool) {
			configs, stop = scanVLResourceConfigs(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		}},
		{"vpclattice:ListRules", 0, func(p *phaseTotals) bool {
			return scanVLRules(ctx, client, acct, region, st, scanID, listeners, serviceOfListener, tags, p)
		}},
		{"vpclattice:ListTargetGroups", 0, func(p *phaseTotals) (stop bool) {
			tgs, stop = scanVLTargetGroups(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		}},
		{"vpclattice:ListAccessLogSubscriptions", 0, func(p *phaseTotals) bool {
			return scanVLAccessLogSubs(ctx, client, acct, region, st, scanID, servicesAndNetworks(), tags, p)
		}},
		countedPhase(0, "vpclattice:GetAuthPolicy", func() (int, int, error) {
			return scanVLAuthPolicies(ctx, client, acct, region, st, scanID, serviceARNs(), networkARNs())
		}),
		countedPhase(0, "vpclattice:GetResourcePolicy", func() (int, int, error) {
			return scanVLResourcePolicies(ctx, client, acct, region, st, scanID, serviceARNs(), networkARNs())
		}),
		{"vpclattice:ListDomainVerifications", 0, func(p *phaseTotals) bool {
			return scanVLDomainVerifications(ctx, client, acct, region, st, scanID, p)
		}},
		{"vpclattice:ListResourceGateways", 0, func(p *phaseTotals) bool {
			return scanVLResourceGateways(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{"vpclattice:ListResourceEndpointAssociations", 0, func(p *phaseTotals) bool {
			return scanVLResourceEndpointAssocs(ctx, client, acct, region, st, scanID, configs, tags, p)
		}},
		{"vpclattice:ListServiceNetworkResourceAssociations", 0, func(p *phaseTotals) bool {
			return scanVLSNRA(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{"vpclattice:ListServiceNetworkServiceAssociations", 0, func(p *phaseTotals) bool {
			return scanVLSNSA(ctx, client, acct, region, st, scanID, networks, tags, p)
		}},
		{"vpclattice:ListServiceNetworkVpcAssociations", 0, func(p *phaseTotals) bool {
			return scanVLSNVA(ctx, client, acct, region, st, scanID, networks, tags, p)
		}},
		countedPhase(0, vlListSNVpcEndpointAssocsOp, func() (int, int, error) {
			return scanVLServiceNetworkVpcEndpointAssocs(ctx, client, acct, region, st, scanID, networks)
		}),
		countedPhase(0, vlListTargetsOp, func() (int, int, error) {
			return scanVLTargets(ctx, client, acct, region, st, scanID, tgs)
		}),
	}
}

// scanVLServices stores the region's services, each as its GetService body
// (which adds the DNS entry, custom domain and certificate the list element
// lacks) with its tags, and returns the stored ones, the parents of the
// listener, access log subscription and policy phases.
func scanVLServices(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (services []childParent, storeWriteFailed bool) {
	const op = "vpclattice:ListServices"
	d := &detailedRows{getOp: "vpclattice:GetService", what: "service(s)", notFound: vlNotFound, tags: tags}
	total, inserted, services, err := storeParentListWith(ctx, st, acct, region, op, "services", "Arn",
		func(ctx context.Context, token *string) ([]types.ServiceSummary, *string, error) {
			out, err := client.ListServices(ctx, &vpclattice.ListServicesInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(s types.ServiceSummary) (*store.Resource, string, string) {
			if sv(s.Arn) == "" {
				return nil, "services have an empty Arn", cmp.Or(sv(s.Name), sv(s.Id), "(a service with no name)")
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeService, sv(s.Arn), cmp.Or(sv(s.Name), sv(s.Id)), s), "", ""
		},
		func(s types.ServiceSummary) string { return sv(s.Arn) },
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetService(ctx, &vpclattice.GetServiceInput{ServiceIdentifier: sdkaws.String(r.NativeID)})
			if otherAccountGap(r.NativeID, acct.ID, err, nil) {
				// A nil body keeps the row as its list element (readRowDetails).
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return out, nil
		}))
	return services, d.addPhase(p, op, total, inserted, err)
}

// scanVLServiceNetworks stores the region's service networks with their tags
// and returns the stored ones, shared-in networks included: ListServiceNetworks
// lists those too, and their associations can name this account's resources.
func scanVLServiceNetworks(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (networks []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "vpclattice:ListServiceNetworks", "service networks", "Arn",
		func(ctx context.Context, token *string) ([]types.ServiceNetworkSummary, *string, error) {
			out, err := client.ListServiceNetworks(ctx, &vpclattice.ListServiceNetworksInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(n types.ServiceNetworkSummary) (*store.Resource, string, string) {
			if sv(n.Arn) == "" {
				return nil, "service networks have an empty Arn", cmp.Or(sv(n.Name), sv(n.Id), "(a service network with no name)")
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeServiceNetwork, sv(n.Arn), cmp.Or(sv(n.Name), sv(n.Id)), n), "", ""
		},
		func(n types.ServiceNetworkSummary) string { return sv(n.Arn) },
		tags, p)
}

// vlStoredParents is the stored rows as the parents of a child phase, keyed
// by their ARN.
func vlStoredParents(rows []*store.Resource) []childParent {
	parents := make([]childParent, len(rows))
	for i, r := range rows {
		parents[i] = childParent{id: r.NativeID, arn: r.NativeID}
	}
	return parents
}

// scanVLListeners stores the listeners of each stored service with their
// tags, records in serviceOfListener the service each listener was listed
// under (ListRules takes both), and returns the stored listeners. A service
// ListListeners no longer finds is skipped.
func scanVLListeners(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, services []childParent, serviceOfListener map[string]string, tags rowTagReader, p *phaseTotals) (listeners []childParent, storeWriteFailed bool) {
	stored, stop := storeEachParentTags(ctx, st, acct, region, "vpclattice:ListListeners", vlNotFound, services,
		vlParentPages(acct.ID, func(ctx context.Context, svc string, token *string) ([]types.ListenerSummary, *string, error) {
			out, err := client.ListListeners(ctx, &vpclattice.ListListenersInput{ServiceIdentifier: &svc, NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(svc string, l types.ListenerSummary) (*store.Resource, string, string) {
			if sv(l.Arn) == "" {
				return nil, "listeners have an empty Arn", cmp.Or(sv(l.Name), sv(l.Id), "(a listener with no name)") + " of service " + svc
			}
			serviceOfListener[sv(l.Arn)] = svc
			return vlRow(acct, region, scanID, TypeVpcLatticeListener, sv(l.Arn), cmp.Or(sv(l.Name), sv(l.Id)), l), "", ""
		}, tags, p)
	return vlStoredParents(stored), stop
}

// scanVLRules stores the rules of each stored listener with their tags. A
// listener ListRules no longer finds is skipped.
func scanVLRules(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, listeners []childParent, serviceOfListener map[string]string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "vpclattice:ListRules", vlNotFound, listeners,
		vlParentPages(acct.ID, func(ctx context.Context, listener string, token *string) ([]types.RuleSummary, *string, error) {
			out, err := client.ListRules(ctx, &vpclattice.ListRulesInput{
				ServiceIdentifier: sdkaws.String(serviceOfListener[listener]), ListenerIdentifier: &listener,
				NextToken: token, MaxResults: sdkaws.Int32(vlPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(listener string, r types.RuleSummary) (*store.Resource, string, string) {
			if sv(r.Arn) == "" {
				return nil, "rules have an empty Arn", cmp.Or(sv(r.Name), sv(r.Id), "(a rule with no name)") + " of listener " + listener
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeRule, sv(r.Arn), cmp.Or(sv(r.Name), sv(r.Id)), r), "", ""
		}, tags, p)
	return stop
}

// scanVLTargetGroups stores the region's target groups with their tags and
// returns the stored ones, the parents of the target phase.
func scanVLTargetGroups(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (tgs []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "vpclattice:ListTargetGroups", "target groups", "Arn",
		func(ctx context.Context, token *string) ([]types.TargetGroupSummary, *string, error) {
			out, err := client.ListTargetGroups(ctx, &vpclattice.ListTargetGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(tg types.TargetGroupSummary) (*store.Resource, string, string) {
			if sv(tg.Arn) == "" {
				return nil, "target groups have an empty Arn", cmp.Or(sv(tg.Name), sv(tg.Id), "(a target group with no name)")
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeTargetGroup, sv(tg.Arn), cmp.Or(sv(tg.Name), sv(tg.Id)), tg), "", ""
		},
		func(tg types.TargetGroupSummary) string { return sv(tg.Arn) },
		tags, p)
}

// scanVLAccessLogSubs stores the access log subscriptions of each stored
// service and service network with their tags.
func scanVLAccessLogSubs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, parents []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "vpclattice:ListAccessLogSubscriptions", nil, parents,
		vlParentPages(acct.ID, func(ctx context.Context, parent string, token *string) ([]types.AccessLogSubscriptionSummary, *string, error) {
			out, err := client.ListAccessLogSubscriptions(ctx, &vpclattice.ListAccessLogSubscriptionsInput{ResourceIdentifier: &parent, NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(parent string, a types.AccessLogSubscriptionSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "access log subscriptions have an empty Arn", cmp.Or(sv(a.Id), "(an access log subscription with no id)") + " of " + parent
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeAccessLogSubscription, sv(a.Arn), sv(a.Id), a), "", ""
		}, tags, p)
	return stop
}

// scanVLAuthPolicies runs GetAuthPolicy per (service, service-network) —
// returns ResourceNotFoundException when no auth policy attached.
func scanVLAuthPolicies(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, svcARNs, netARNs []string) (int, int, error) {
	var batch []*store.Resource
	for _, ra := range append(append([]string{}, svcARNs...), netARNs...) {
		id := ra
		out, err := client.GetAuthPolicy(ctx, &vpclattice.GetAuthPolicyInput{ResourceIdentifier: &id})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("vpclattice:GetAuthPolicy %s: %w", ra, err)
		}
		if out.Policy == nil {
			continue
		}
		arn := ra + "/auth-policy"
		name := "auth-policy"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeVpcLatticeAuthPolicy, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "vpclattice auth-policies")
}

func scanVLResourcePolicies(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, svcARNs, netARNs []string) (int, int, error) {
	var batch []*store.Resource
	for _, ra := range append(append([]string{}, svcARNs...), netARNs...) {
		id := ra
		out, err := client.GetResourcePolicy(ctx, &vpclattice.GetResourcePolicyInput{ResourceArn: &id})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("vpclattice:GetResourcePolicy %s: %w", ra, err)
		}
		if out.Policy == nil {
			continue
		}
		arn := ra + "/resource-policy"
		name := "resource-policy"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeVpcLatticeResourcePolicy, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "vpclattice resource-policies")
}

// scanVLDomainVerifications stores the region's domain verifications with
// the tags their list element carries.
func scanVLDomainVerifications(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	const op = "vpclattice:ListDomainVerifications"
	total, inserted, err := storeTokenList(ctx, st, acct, region, op,
		func(ctx context.Context, token *string) ([]types.DomainVerificationSummary, *string, error) {
			out, err := client.ListDomainVerifications(ctx, &vpclattice.ListDomainVerificationsInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(d types.DomainVerificationSummary) (*store.Resource, string, string) {
			if sv(d.Arn) == "" {
				return nil, "domain verifications have an empty Arn", cmp.Or(sv(d.DomainName), sv(d.Id), "(a domain verification with no name)")
			}
			r := vlRow(acct, region, scanID, TypeVpcLatticeDomainVerification, sv(d.Arn), cmp.Or(sv(d.DomainName), sv(d.Id)), d)
			r.TagsJSON = mapTagsJSON(d.Tags)
			return r, "", ""
		})
	return p.addPhase(op, total, inserted, err)
}

// scanVLResourceConfigs stores the region's resource configurations with
// their tags and returns the stored ones, the parents of the resource
// endpoint association phase.
func scanVLResourceConfigs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (configs []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "vpclattice:ListResourceConfigurations", "resource configurations", "Arn",
		func(ctx context.Context, token *string) ([]types.ResourceConfigurationSummary, *string, error) {
			out, err := client.ListResourceConfigurations(ctx, &vpclattice.ListResourceConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(c types.ResourceConfigurationSummary) (*store.Resource, string, string) {
			if sv(c.Arn) == "" {
				return nil, "resource configurations have an empty Arn", cmp.Or(sv(c.Name), sv(c.Id), "(a resource configuration with no name)")
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeResourceConfiguration, sv(c.Arn), cmp.Or(sv(c.Name), sv(c.Id)), c), "", ""
		},
		func(c types.ResourceConfigurationSummary) string { return sv(c.Arn) },
		tags, p)
}

// scanVLResourceEndpointAssocs stores the VPC endpoint associations of each
// stored resource configuration with their tags.
func scanVLResourceEndpointAssocs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, configs []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "vpclattice:ListResourceEndpointAssociations", nil, configs,
		vlParentPages(acct.ID, func(ctx context.Context, config string, token *string) ([]types.ResourceEndpointAssociationSummary, *string, error) {
			out, err := client.ListResourceEndpointAssociations(ctx, &vpclattice.ListResourceEndpointAssociationsInput{
				ResourceConfigurationIdentifier: &config, NextToken: token, MaxResults: sdkaws.Int32(vlPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(config string, a types.ResourceEndpointAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "resource endpoint associations have an empty Arn", cmp.Or(sv(a.Id), "(an association with no id)") + " of resource configuration " + config
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeResourceEndpointAssociation, sv(a.Arn), sv(a.Id), a), "", ""
		}, tags, p)
	return stop
}

// scanVLResourceGateways stores the region's resource gateways with their
// tags.
func scanVLResourceGateways(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "vpclattice:ListResourceGateways",
		func(ctx context.Context, token *string) ([]types.ResourceGatewaySummary, *string, error) {
			out, err := client.ListResourceGateways(ctx, &vpclattice.ListResourceGatewaysInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(g types.ResourceGatewaySummary) (*store.Resource, string, string) {
			if sv(g.Arn) == "" {
				return nil, "resource gateways have an empty Arn", cmp.Or(sv(g.Name), sv(g.Id), "(a resource gateway with no name)")
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeResourceGateway, sv(g.Arn), cmp.Or(sv(g.Name), sv(g.Id)), g), "", ""
		},
		tags, p)
}

// scanVLSNRA stores the region's service network resource associations
// with their tags.
func scanVLSNRA(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "vpclattice:ListServiceNetworkResourceAssociations",
		func(ctx context.Context, token *string) ([]types.ServiceNetworkResourceAssociationSummary, *string, error) {
			out, err := client.ListServiceNetworkResourceAssociations(ctx, &vpclattice.ListServiceNetworkResourceAssociationsInput{NextToken: token, MaxResults: sdkaws.Int32(vlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(a types.ServiceNetworkResourceAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "service network resource associations have an empty Arn", cmp.Or(sv(a.Id), "(an association with no id)")
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeServiceNetworkResourceAssociation, sv(a.Arn), sv(a.Id), a), "", ""
		},
		tags, p)
}

// scanVLSNSA stores the service associations of each stored service network
// with their tags. The op lists by service network or by service; each
// network is asked.
func scanVLSNSA(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "vpclattice:ListServiceNetworkServiceAssociations", nil, networks,
		vlParentPages(acct.ID, func(ctx context.Context, network string, token *string) ([]types.ServiceNetworkServiceAssociationSummary, *string, error) {
			out, err := client.ListServiceNetworkServiceAssociations(ctx, &vpclattice.ListServiceNetworkServiceAssociationsInput{
				ServiceNetworkIdentifier: &network, NextToken: token, MaxResults: sdkaws.Int32(vlPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(network string, a types.ServiceNetworkServiceAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "service network service associations have an empty Arn", cmp.Or(sv(a.Id), "(an association with no id)") + " of service network " + network
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeServiceNetworkServiceAssociation, sv(a.Arn), sv(a.Id), a), "", ""
		}, tags, p)
	return stop
}

// scanVLSNVA stores the VPC associations of each stored service network
// with their tags. The op lists by service network or by VPC; each network
// is asked.
func scanVLSNVA(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "vpclattice:ListServiceNetworkVpcAssociations", nil, networks,
		vlParentPages(acct.ID, func(ctx context.Context, network string, token *string) ([]types.ServiceNetworkVpcAssociationSummary, *string, error) {
			out, err := client.ListServiceNetworkVpcAssociations(ctx, &vpclattice.ListServiceNetworkVpcAssociationsInput{
				ServiceNetworkIdentifier: &network, NextToken: token, MaxResults: sdkaws.Int32(vlPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(network string, a types.ServiceNetworkVpcAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "service network VPC associations have an empty Arn", cmp.Or(sv(a.Id), "(an association with no id)") + " of service network " + network
			}
			return vlRow(acct, region, scanID, TypeVpcLatticeServiceNetworkVpcAssociation, sv(a.Arn), sv(a.Id), a), "", ""
		}, tags, p)
	return stop
}
