package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/vpclattice"
)

func init() {
	registerType(restype.Descriptor{Type: TypeVpcLatticeService, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetwork, Service: "vpclattice", Leaf: true})
	registerType(restype.Descriptor{Type: TypeVpcLatticeListener, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeRule, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeTargetGroup, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeAccessLogSubscription, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeAuthPolicy, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourcePolicy, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeDomainVerification, Service: "vpclattice", Leaf: true})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourceConfiguration, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourceGateway, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkResourceAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkServiceAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkVpcAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeResourceEndpointAssociation, Service: "vpclattice", Upstream: "AWS::vpc-lattice::ResourceEndpointAssociation"})
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
}

func scanVpcLattice(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := vpclattice.NewFromConfig(acct.cfg, func(o *vpclattice.Options) { o.Region = region })

	svcARNs, t, i, ferr := scanVLServices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	netARNs, t, i, ferr := scanVLServiceNetworks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	listenerKeys, t, i, ferr := scanVLListeners(ctx, client, acct, region, st, scanID, svcARNs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	rcARNs, t, i, ferr := scanVLResourceConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanVLRules(ctx, client, acct, region, st, scanID, listenerKeys) },
		func() (int, int, error) { return scanVLTargetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanVLAccessLogSubs(ctx, client, acct, region, st, scanID, svcARNs, netARNs)
		},
		func() (int, int, error) {
			return scanVLAuthPolicies(ctx, client, acct, region, st, scanID, svcARNs, netARNs)
		},
		func() (int, int, error) {
			return scanVLResourcePolicies(ctx, client, acct, region, st, scanID, svcARNs, netARNs)
		},
		func() (int, int, error) { return scanVLDomainVerifications(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanVLResourceGateways(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanVLResourceEndpointAssocs(ctx, client, acct, region, st, scanID, rcARNs)
		},
		func() (int, int, error) { return scanVLSNRA(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanVLSNSA(ctx, client, acct, region, st, scanID, netARNs) },
		func() (int, int, error) { return scanVLSNVA(ctx, client, acct, region, st, scanID, netARNs) },
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

// vlListenerKey carries (serviceArn, listenerArn) — ListRules requires
// both ServiceIdentifier and ListenerIdentifier.
type vlListenerKey struct {
	svcARN string
	lstARN string
}

func scanVLServices(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := vpclattice.NewListServicesPaginator(client, &vpclattice.ListServicesInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListServices", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("vpclattice:ListServices: %w", perr)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			attrsJSON := mustJSON(s)
			if gout, gerr := client.GetService(ctx, &vpclattice.GetServiceInput{ServiceIdentifier: s.Id}); gerr == nil {
				attrsJSON = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeService, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "vpclattice services")
	return arns, t, i, err
}

func scanVLServiceNetworks(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := vpclattice.NewListServiceNetworksPaginator(client, &vpclattice.ListServiceNetworksInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListServiceNetworks", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("vpclattice:ListServiceNetworks: %w", perr)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeServiceNetwork, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "vpclattice service-networks")
	return arns, t, i, err
}

func scanVLListeners(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, svcARNs []string) ([]vlListenerKey, int, int, error) {
	var keys []vlListenerKey
	var batch []*store.Resource
	for _, svcARN := range svcARNs {
		sa := svcARN
		pager := vpclattice.NewListListenersPaginator(client, &vpclattice.ListListenersInput{ServiceIdentifier: &sa})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("vpclattice:ListListeners %s: %w", svcARN, perr)
			}
			for _, l := range out.Items {
				arn := sv(l.Arn)
				if arn == "" {
					continue
				}
				keys = append(keys, vlListenerKey{svcARN: svcARN, lstARN: arn})
				label := sv(l.Name)
				if label == "" {
					label = sv(l.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeVpcLatticeListener, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "vpclattice listeners")
	return keys, t, i, err
}

func scanVLRules(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, keys []vlListenerKey) (int, int, error) {
	var batch []*store.Resource
	for _, k := range keys {
		sa := k.svcARN
		la := k.lstARN
		pager := vpclattice.NewListRulesPaginator(client, &vpclattice.ListRulesInput{ServiceIdentifier: &sa, ListenerIdentifier: &la})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("vpclattice:ListRules %s/%s: %w", k.svcARN, k.lstARN, perr)
			}
			for _, r := range out.Items {
				arn := sv(r.Arn)
				if arn == "" {
					continue
				}
				label := sv(r.Name)
				if label == "" {
					label = sv(r.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeVpcLatticeRule, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "vpclattice rules")
}

func scanVLTargetGroups(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := vpclattice.NewListTargetGroupsPaginator(client, &vpclattice.ListTargetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListTargetGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("vpclattice:ListTargetGroups: %w", perr)
		}
		for _, tg := range out.Items {
			arn := sv(tg.Arn)
			if arn == "" {
				continue
			}
			label := sv(tg.Name)
			if label == "" {
				label = sv(tg.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeTargetGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(tg), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "vpclattice target-groups")
}

// scanVLAccessLogSubs runs per (service or service-network) — each can
// have its own access log subscription.
func scanVLAccessLogSubs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, svcARNs, netARNs []string) (int, int, error) {
	var batch []*store.Resource
	for _, ra := range append(append([]string{}, svcARNs...), netARNs...) {
		id := ra
		pager := vpclattice.NewListAccessLogSubscriptionsPaginator(client, &vpclattice.ListAccessLogSubscriptionsInput{ResourceIdentifier: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("vpclattice:ListAccessLogSubscriptions %s: %w", ra, perr)
			}
			for _, a := range out.Items {
				arn := sv(a.Arn)
				if arn == "" {
					continue
				}
				label := sv(a.Id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeVpcLatticeAccessLogSubscription, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "vpclattice access-log-subscriptions")
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

func scanVLDomainVerifications(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := vpclattice.NewListDomainVerificationsPaginator(client, &vpclattice.ListDomainVerificationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListDomainVerifications", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("vpclattice:ListDomainVerifications: %w", perr)
		}
		for _, d := range out.Items {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.DomainName)
			if label == "" {
				label = sv(d.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeDomainVerification, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "vpclattice domain-verifications")
}

func scanVLResourceConfigs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := vpclattice.NewListResourceConfigurationsPaginator(client, &vpclattice.ListResourceConfigurationsInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListResourceConfigurations", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("vpclattice:ListResourceConfigurations: %w", perr)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeResourceConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "vpclattice resource-configurations")
	return arns, t, i, err
}

// scanVLResourceEndpointAssocs fans out ListResourceEndpointAssociations per
// resource-configuration (op requires ResourceConfigurationIdentifier).
// NativeID = the VPC endpoint association ARN.
func scanVLResourceEndpointAssocs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, rcARNs []string) (int, int, error) {
	if len(rcARNs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, rcARN := range rcARNs {
		id := rcARN
		pager := vpclattice.NewListResourceEndpointAssociationsPaginator(client, &vpclattice.ListResourceEndpointAssociationsInput{ResourceConfigurationIdentifier: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("vpclattice:ListResourceEndpointAssociations %s: %w", rcARN, perr)
			}
			for _, a := range out.Items {
				arn := sv(a.Arn)
				if arn == "" {
					continue
				}
				label := sv(a.Id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeVpcLatticeResourceEndpointAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "vpclattice resource-endpoint-associations")
}

func scanVLResourceGateways(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := vpclattice.NewListResourceGatewaysPaginator(client, &vpclattice.ListResourceGatewaysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListResourceGateways", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("vpclattice:ListResourceGateways: %w", perr)
		}
		for _, g := range out.Items {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeResourceGateway, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "vpclattice resource-gateways")
}

func scanVLSNRA(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := vpclattice.NewListServiceNetworkResourceAssociationsPaginator(client, &vpclattice.ListServiceNetworkResourceAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "vpclattice:ListServiceNetworkResourceAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("vpclattice:ListServiceNetworkResourceAssociations: %w", perr)
		}
		for _, a := range out.Items {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVpcLatticeServiceNetworkResourceAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "vpclattice service-network-resource-associations")
}

// scanVLSNSA requires ServiceNetworkIdentifier or ServiceIdentifier per call.
// Fans out across service-network ARNs from scanVLServiceNetworks.
func scanVLSNSA(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, netARNs []string) (int, int, error) {
	if len(netARNs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, na := range netARNs {
		netARN := na
		pager := vpclattice.NewListServiceNetworkServiceAssociationsPaginator(client, &vpclattice.ListServiceNetworkServiceAssociationsInput{
			ServiceNetworkIdentifier: &netARN,
		})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "vpclattice:ListServiceNetworkServiceAssociations", acct.ID, region, perr)
					return 0, 0, nil
				}
				return 0, 0, fmt.Errorf("vpclattice:ListServiceNetworkServiceAssociations %s: %w", netARN, perr)
			}
			for _, a := range out.Items {
				arn := sv(a.Arn)
				if arn == "" {
					continue
				}
				label := sv(a.Id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeVpcLatticeServiceNetworkServiceAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "vpclattice service-network-service-associations")
}

// scanVLSNVA requires ServiceNetworkIdentifier or VpcIdentifier per call.
// Fans out across service-network ARNs from scanVLServiceNetworks.
func scanVLSNVA(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, netARNs []string) (int, int, error) {
	if len(netARNs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, na := range netARNs {
		netARN := na
		pager := vpclattice.NewListServiceNetworkVpcAssociationsPaginator(client, &vpclattice.ListServiceNetworkVpcAssociationsInput{
			ServiceNetworkIdentifier: &netARN,
		})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "vpclattice:ListServiceNetworkVpcAssociations", acct.ID, region, perr)
					return 0, 0, nil
				}
				return 0, 0, fmt.Errorf("vpclattice:ListServiceNetworkVpcAssociations %s: %w", netARN, perr)
			}
			for _, a := range out.Items {
				arn := sv(a.Arn)
				if arn == "" {
					continue
				}
				label := sv(a.Id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeVpcLatticeServiceNetworkVpcAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "vpclattice service-network-vpc-associations")
}
