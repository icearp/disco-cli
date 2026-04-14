package aws

import (
	"context"
	"fmt"
	"strconv"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

func init() {
	registerService(serviceEntry{name: "aws:elasticloadbalancingv2", fn: scanELBv2})
}

// scanELBv2 discovers all ELBv2 resource types in one region:
// load balancers, listeners, listener rules, listener certificates,
// target groups, trust stores, and trust store revocations.
func scanELBv2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elasticloadbalancingv2.NewFromConfig(acct.cfg, func(o *elasticloadbalancingv2.Options) { o.Region = region })

	// Load balancers — returns ARNs for listener enumeration
	lbARNs, t, i, err := scanELBv2LoadBalancers(ctx, client, acct, region, st, scanID)
	if err != nil {
		return 0, 0, err
	}
	total += t
	inserted += i

	// Target groups are independent of load balancers
	t, i, err = scanELBv2TargetGroups(ctx, client, acct, region, st, scanID)
	if err != nil {
		return 0, 0, err
	}
	total += t
	inserted += i

	// Trust stores and their revocations
	tsARNs, t, i, err := scanELBv2TrustStores(ctx, client, acct, region, st, scanID)
	if err != nil {
		return 0, 0, err
	}
	total += t
	inserted += i
	for _, tsARN := range tsARNs {
		t, i, err = scanELBv2TrustStoreRevocations(ctx, client, acct, region, tsARN, st, scanID)
		if err != nil {
			return 0, 0, err
		}
		total += t
		inserted += i
	}

	// Listeners and their rules/certificates
	for _, lbARN := range lbARNs {
		listenerARNs, t, i, err := scanELBv2Listeners(ctx, client, acct, region, lbARN, st, scanID)
		if err != nil {
			return 0, 0, err
		}
		total += t
		inserted += i
		for _, listenerARN := range listenerARNs {
			t, i, err = scanELBv2ListenerRules(ctx, client, acct, region, listenerARN, st, scanID)
			if err != nil {
				return 0, 0, err
			}
			total += t
			inserted += i
			t, i, err = scanELBv2ListenerCertificates(ctx, client, acct, region, listenerARN, st, scanID)
			if err != nil {
				return 0, 0, err
			}
			total += t
			inserted += i
		}
	}

	return total, inserted, nil
}

// scanELBv2LoadBalancers pages through all ALBs, NLBs, and GLBs.
// Returns the list of ARNs for downstream listener enumeration.
func scanELBv2LoadBalancers(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region string, st *store.Store, scanID string) (arns []string, total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeLoadBalancers", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("elasticloadbalancing:DescribeLoadBalancers: %w", err)
		}
		var batch []*store.Resource
		for _, lb := range page.LoadBalancers {
			arn := sv(lb.LoadBalancerArn)
			arns = append(arns, arn)
			name := sv(lb.LoadBalancerName)
			status := string(lb.State.Code)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2LoadBalancer,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(lb.CreatedTime),
				Status:         &status,
				AttributesJSON: mustJSON(map[string]any{"lb": lb, "type": string(lb.Type)}),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("upsert load balancers: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return arns, total, inserted, nil
}

// scanELBv2Listeners pages through listeners for one load balancer.
// Returns listener ARNs for rule/certificate enumeration.
func scanELBv2Listeners(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region, lbARN string, st *store.Store, scanID string) (arns []string, total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeListenersPaginator(client, &elasticloadbalancingv2.DescribeListenersInput{
		LoadBalancerArn: &lbARN,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeListeners", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("elasticloadbalancing:DescribeListeners: %w", err)
		}
		var batch []*store.Resource
		for _, l := range page.Listeners {
			arn := sv(l.ListenerArn)
			arns = append(arns, arn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2Listener,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(l),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("upsert listeners: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return arns, total, inserted, nil
}

// scanELBv2ListenerRules pages through rules for one listener.
// The Rule struct omits the listener ARN, so we inject it into the stored attrs.
func scanELBv2ListenerRules(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region, listenerARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeRulesPaginator(client, &elasticloadbalancingv2.DescribeRulesInput{
		ListenerArn: &listenerARN,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("elasticloadbalancing:DescribeRules: %w", err)
		}
		var batch []*store.Resource
		for _, rule := range page.Rules {
			ruleARN := sv(rule.RuleArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2ListenerRule,
				NativeID:       ruleARN,
				Region:         &region,
				AttributesJSON: mustJSON(map[string]any{"rule": rule, "listenerArn": listenerARN}),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert listener rules: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanELBv2ListenerCertificates pages through certificates for one listener.
// ListenerCertificate has no dedicated ARN; we synthesise one as listenerARN+":cert/"+CertificateArn.
func scanELBv2ListenerCertificates(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region, listenerARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeListenerCertificatesPaginator(client, &elasticloadbalancingv2.DescribeListenerCertificatesInput{
		ListenerArn: &listenerARN,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeListenerCertificates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("elasticloadbalancing:DescribeListenerCertificates: %w", err)
		}
		var batch []*store.Resource
		for _, cert := range page.Certificates {
			nativeID := listenerARN + ":cert/" + sv(cert.CertificateArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2ListenerCertificate,
				NativeID:       nativeID,
				Region:         &region,
				AttributesJSON: mustJSON(map[string]any{"listenerArn": listenerARN, "cert": cert}),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert listener certificates: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanELBv2TargetGroups pages through all target groups (independent of LBs).
func scanELBv2TargetGroups(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeTargetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("elasticloadbalancing:DescribeTargetGroups: %w", err)
		}
		var batch []*store.Resource
		for _, tg := range page.TargetGroups {
			arn := sv(tg.TargetGroupArn)
			name := sv(tg.TargetGroupName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2TargetGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(tg),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert target groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanELBv2TrustStores pages through all MTLS trust stores.
// Returns ARNs for revocation enumeration.
func scanELBv2TrustStores(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region string, st *store.Store, scanID string) (arns []string, total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeTrustStoresPaginator(client, &elasticloadbalancingv2.DescribeTrustStoresInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeTrustStores", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("elasticloadbalancing:DescribeTrustStores: %w", err)
		}
		var batch []*store.Resource
		for _, ts := range page.TrustStores {
			arn := sv(ts.TrustStoreArn)
			arns = append(arns, arn)
			name := sv(ts.Name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2TrustStore,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(ts),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("upsert trust stores: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return arns, total, inserted, nil
}

// scanELBv2TrustStoreRevocations pages through revocations for one trust store.
// TrustStoreRevocation has no dedicated ARN; NativeID = tsARN+":rev/"+RevocationId.
func scanELBv2TrustStoreRevocations(ctx context.Context, client *elasticloadbalancingv2.Client, acct *account, region, tsARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticloadbalancingv2.NewDescribeTrustStoreRevocationsPaginator(client, &elasticloadbalancingv2.DescribeTrustStoreRevocationsInput{
		TrustStoreArn: &tsARN,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeTrustStoreRevocations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("elasticloadbalancing:DescribeTrustStoreRevocations: %w", err)
		}
		var batch []*store.Resource
		for _, rev := range page.TrustStoreRevocations {
			var revID int64
			if rev.RevocationId != nil {
				revID = *rev.RevocationId
			}
			nativeID := tsARN + ":rev/" + strconv.FormatInt(revID, 10)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBv2TrustStoreRevocation,
				NativeID:       nativeID,
				Region:         &region,
				AttributesJSON: mustJSON(rev),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert trust store revocations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
