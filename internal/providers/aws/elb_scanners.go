package aws

import (
	"context"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeELBv2LoadBalancer, Service: "elasticloadbalancing"})
	registerType(restype.Descriptor{Type: TypeELBv2Listener, Service: "elasticloadbalancing"})
	registerType(restype.Descriptor{Type: TypeELBv2ListenerCertificate, Service: "elasticloadbalancing"})
	registerType(restype.Descriptor{Type: TypeELBv2ListenerRule, Service: "elasticloadbalancing"})
	registerType(restype.Descriptor{Type: TypeELBv2TargetGroup, Service: "elasticloadbalancing"})
	registerType(restype.Descriptor{Type: TypeELBv2TrustStore, Service: "elasticloadbalancing"})
	registerType(restype.Descriptor{Type: TypeELBv2TrustStoreRevocation, Service: "elasticloadbalancing"})
	registerService(serviceEntry{
		name: "aws:elasticloadbalancingv2",
		fn:   scanELBv2,
	})
}

// elbv2API is the narrow set of ELBv2 ops called by scanELBv2's sub-phases.
type elbv2API interface {
	DescribeLoadBalancers(context.Context, *elasticloadbalancingv2.DescribeLoadBalancersInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error)
	DescribeListeners(context.Context, *elasticloadbalancingv2.DescribeListenersInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeListenersOutput, error)
	DescribeRules(context.Context, *elasticloadbalancingv2.DescribeRulesInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeRulesOutput, error)
	DescribeListenerCertificates(context.Context, *elasticloadbalancingv2.DescribeListenerCertificatesInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeListenerCertificatesOutput, error)
	DescribeTargetGroups(context.Context, *elasticloadbalancingv2.DescribeTargetGroupsInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error)
	DescribeTargetHealth(context.Context, *elasticloadbalancingv2.DescribeTargetHealthInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error)
	DescribeTrustStores(context.Context, *elasticloadbalancingv2.DescribeTrustStoresInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTrustStoresOutput, error)
	DescribeTrustStoreRevocations(context.Context, *elasticloadbalancingv2.DescribeTrustStoreRevocationsInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTrustStoreRevocationsOutput, error)
	DescribeTags(context.Context, *elasticloadbalancingv2.DescribeTagsInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error)
}

const (
	// elbTagsOp is the tag op of both ELB SDK packages, under the one IAM
	// action both sign with.
	elbTagsOp = "elasticloadbalancing:DescribeTags"
	// elbPageCap is the PageSize maximum of every ELB listing read here.
	elbPageCap = 400
	// elbTagBatch is how many resources one DescribeTags call takes: the
	// documented maximum of ResourceArns (v2) and the length maximum of
	// LoadBalancerNames (classic).
	elbTagBatch = 20
)

// elbv2TagsGone is the not-found codes elbv2 DescribeTags models: one per
// resource type it reads.
var elbv2TagsGone = []string{"ListenerNotFound", "LoadBalancerNotFound", "RuleNotFound", "TargetGroupNotFound", "TrustStoreNotFound"}

// elbv2TGWithTargets is a target group's stored attributes: the listed
// element and the targets DescribeTargetHealth returned for it.
type elbv2TGWithTargets struct {
	TargetGroup any   `json:"TargetGroup"`
	Targets     []any `json:"Targets"`
}

// scanELBv2 discovers the ELBv2 resources of one region: load balancers,
// target groups, trust stores, each trust store's revocations, each load
// balancer's listeners, and each listener's rules and certificates. Every
// listing runs as its own phase, one at a time: a phase's error does not
// stop the later ones; a failed write and ctx's end do (runHostPhases). The
// child phases read the parents stored this scan, those parents
// concurrently.
//
// Load balancers, target groups, trust stores, listeners and rules have
// their tags read with DescribeTags, 20 resources a call, as each page is
// listed (elbv2Tags); its denials are one warning for the whole scan.
// Listener certificates and trust store revocations take no tags: the
// Service Reference has no ARN format for either, AddTags lists neither, and
// their NativeIDs are built here.
func scanELBv2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := elasticloadbalancingv2.NewFromConfig(acct.cfg, func(o *elasticloadbalancingv2.Options) { o.Region = region })
	bulk := elbv2Tags(client)
	tags := bulk.bulkTagReader().sharingDenials()
	var lbs, listeners, trustStores []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "elasticloadbalancingv2", 0, []hostPhase{
		{op: "elasticloadbalancing:DescribeLoadBalancers", run: func(p *phaseTotals) (failed bool) {
			lbs, failed = scanELBv2LoadBalancers(ctx, client, acct, region, st, scanID, bulk, tags, p)
			return failed
		}},
		{op: "elasticloadbalancing:DescribeTargetGroups", run: func(p *phaseTotals) bool {
			return scanELBv2TargetGroups(ctx, client, acct, region, st, scanID, bulk, tags, p)
		}},
		{op: "elasticloadbalancing:DescribeTrustStores", run: func(p *phaseTotals) (failed bool) {
			trustStores, failed = scanELBv2TrustStores(ctx, client, acct, region, st, scanID, bulk, tags, p)
			return failed
		}},
		countedPhase(0, "elasticloadbalancing:DescribeTrustStoreRevocations", func() (int, int, error) {
			return scanELBv2TrustStoreRevocations(ctx, client, acct, region, st, scanID, trustStores)
		}),
		{op: "elasticloadbalancing:DescribeListeners", run: func(p *phaseTotals) (failed bool) {
			listeners, failed = scanELBv2Listeners(ctx, client, acct, region, st, scanID, lbs, bulk, tags, p)
			return failed
		}},
		{op: "elasticloadbalancing:DescribeRules", run: func(p *phaseTotals) bool {
			return scanELBv2ListenerRules(ctx, client, acct, region, st, scanID, listeners, bulk, tags, p)
		}},
		countedPhase(0, "elasticloadbalancing:DescribeListenerCertificates", func() (int, int, error) {
			return scanELBv2ListenerCertificates(ctx, client, acct, region, st, scanID, listeners)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// elbv2Tags reads the tags of the rows of every tagged type with
// DescribeTags, sent the ARN each row's listing returned (its NativeID). The
// op's model lists a not-found error for each of those types (load balancer,
// target group, trust store, listener, rule), and AddTags in the Service
// Reference takes each of them; the Service Reference lists no resource for
// DescribeTags itself.
func elbv2Tags(client elbv2API) *bulkRowTags {
	return newBulkRowTags(elbTagsOp, elbTagBatch, elbv2TagsGone,
		func(r *store.Resource) string { return r.NativeID },
		func(ctx context.Context, arns []string) (map[string]*string, error) {
			out, err := client.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: arns})
			if err != nil {
				return nil, err
			}
			got := make(map[string]*string, len(out.TagDescriptions))
			for _, d := range out.TagDescriptions {
				got[sv(d.ResourceArn)] = kvTagsJSON(d.Tags, func(t elbv2types.Tag) (*string, *string) { return t.Key, t.Value })
			}
			return got, nil
		})
}

// elbv2Row is the row of a listed element stored under its ARN. An element
// without an ARN is not stored (arnRow); what names the elements in that
// warning, and key identifies the element there: its name, or for a listener
// or rule, which have no name, its parent's ARN.
func elbv2Row(acct *account, region, scanID, typ, what string, arn, key *string, attrs any) (*store.Resource, string, string) {
	return arnRow(what, arn, key, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Region: &region, AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	})
}

// scanELBv2LoadBalancers stores every ALB, NLB and GLB with its tags, a page
// at a time, and returns those stored, by ARN, for the listener phase.
func scanELBv2LoadBalancers(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, bulk *bulkRowTags, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "elasticloadbalancing:DescribeLoadBalancers", "load balancers", "LoadBalancerArn",
		func(ctx context.Context, marker *string) ([]elbv2types.LoadBalancer, *string, error) {
			out, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			fetchItemTags(ctx, bulk, out.LoadBalancers, func(lb elbv2types.LoadBalancer) *string { return lb.LoadBalancerArn })
			return out.LoadBalancers, out.NextMarker, nil
		},
		func(lb elbv2types.LoadBalancer) (*store.Resource, string, string) {
			r, why, key := elbv2Row(acct, region, scanID, TypeELBv2LoadBalancer, "load balancers", lb.LoadBalancerArn, lb.LoadBalancerName,
				map[string]any{"lb": lb, "type": string(lb.Type)})
			if r != nil {
				name := sv(lb.LoadBalancerName)
				r.Name = &name
				r.CreatedAt = tp(lb.CreatedTime)
				// State is optional in the model.
				if lb.State != nil {
					code := string(lb.State.Code)
					r.Status = &code
				}
			}
			return r, why, key
		},
		func(lb elbv2types.LoadBalancer) string { return sv(lb.LoadBalancerArn) },
		tags, p)
}

// scanELBv2TargetGroups stores every target group with its registered
// targets and its tags, a page at a time. The targets come from one
// DescribeTargetHealth per target group, read concurrently: a target group
// it reports not found was deleted since it was listed and is not stored;
// any other failure stores the target group without targets, a denial with
// one warning and any other error returned.
func scanELBv2TargetGroups(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, bulk *bulkRowTags, tags rowTagReader, p *phaseTotals) bool {
	// listed maps each row of the page being stored to its element, for the
	// DescribeTargetHealth reads, which only read it. It is emptied as each
	// page is fetched, which bounds its memory to one page; nothing else
	// depends on that, so no test observes it.
	listed := map[*store.Resource]elbv2types.TargetGroup{}
	return storeTokenPagesTagsWith(ctx, st, acct, region, "elasticloadbalancing:DescribeTargetGroups",
		func(ctx context.Context, marker *string) ([]elbv2types.TargetGroup, *string, error) {
			clear(listed)
			out, err := client.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			fetchItemTags(ctx, bulk, out.TargetGroups, func(tg elbv2types.TargetGroup) *string { return tg.TargetGroupArn })
			return out.TargetGroups, out.NextMarker, nil
		},
		func(tg elbv2types.TargetGroup) (*store.Resource, string, string) {
			r, why, key := elbv2Row(acct, region, scanID, TypeELBv2TargetGroup, "target groups", tg.TargetGroupArn, tg.TargetGroupName,
				elbv2TGWithTargets{TargetGroup: tg})
			if r != nil {
				name := sv(tg.TargetGroupName)
				r.Name = &name
				listed[r] = tg
			}
			return r, why, key
		},
		tokenPageSteps{
			details: rowDetailReader{
				op: "elasticloadbalancing:DescribeTargetHealth", what: "target groups",
				notFound: []string{"TargetGroupNotFound"},
				get: func(ctx context.Context, r *store.Resource) (any, error) {
					out, err := client.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{TargetGroupArn: &r.NativeID})
					if err != nil {
						return nil, err
					}
					var targets []any
					for _, d := range out.TargetHealthDescriptions {
						if d.Target != nil {
							targets = append(targets, d.Target)
						}
					}
					return elbv2TGWithTargets{TargetGroup: listed[r], Targets: targets}, nil
				},
			},
		},
		tags, p)
}

// scanELBv2TrustStores stores every mTLS trust store with its tags, a page
// at a time, and returns those stored, by ARN, for the revocation phase.
func scanELBv2TrustStores(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, bulk *bulkRowTags, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "elasticloadbalancing:DescribeTrustStores", "trust stores", "TrustStoreArn",
		func(ctx context.Context, marker *string) ([]elbv2types.TrustStore, *string, error) {
			out, err := client.DescribeTrustStores(ctx, &elasticloadbalancingv2.DescribeTrustStoresInput{Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			fetchItemTags(ctx, bulk, out.TrustStores, func(ts elbv2types.TrustStore) *string { return ts.TrustStoreArn })
			return out.TrustStores, out.NextMarker, nil
		},
		func(ts elbv2types.TrustStore) (*store.Resource, string, string) {
			r, why, key := elbv2Row(acct, region, scanID, TypeELBv2TrustStore, "trust stores", ts.TrustStoreArn, ts.Name, ts)
			if r != nil {
				name := sv(ts.Name)
				r.Name = &name
			}
			return r, why, key
		},
		func(ts elbv2types.TrustStore) string { return sv(ts.TrustStoreArn) },
		tags, p)
}

// scanELBv2TrustStoreRevocations stores the revocations of each trust store
// stored this scan. A revocation has no ARN; its NativeID is the trust
// store's ARN + ":rev/" + RevocationId, and one without a RevocationId is not
// stored.
func scanELBv2TrustStoreRevocations(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, trustStores []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "elasticloadbalancing:DescribeTrustStoreRevocations", []string{"TrustStoreNotFound"}, childParentIDs(trustStores),
		func(ctx context.Context, tsARN string, marker *string) ([]elbv2types.DescribeTrustStoreRevocation, *string, error) {
			out, err := client.DescribeTrustStoreRevocations(ctx, &elasticloadbalancingv2.DescribeTrustStoreRevocationsInput{TrustStoreArn: &tsARN, Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TrustStoreRevocations, out.NextMarker, nil
		},
		func(tsARN string, rev elbv2types.DescribeTrustStoreRevocation) (*store.Resource, string, string) {
			if rev.RevocationId == nil {
				return nil, "trust store revocations have no RevocationId", tsARN
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeELBv2TrustStoreRevocation, NativeID: tsARN + ":rev/" + strconv.FormatInt(*rev.RevocationId, 10),
				Region: &region, AttributesJSON: mustJSON(rev), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanELBv2Listeners stores the listeners of each load balancer stored this
// scan, with their tags, a page at a time, and returns those stored, by
// ARN, for the rule and certificate phases.
func scanELBv2Listeners(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, lbs []childParent, bulk *bulkRowTags, tags rowTagReader, p *phaseTotals) (listeners []childParent, failed bool) {
	failed = storeEachParentPagesTagsWith(ctx, st, acct, region, "elasticloadbalancing:DescribeListeners", []string{"LoadBalancerNotFound"}, lbs,
		func(ctx context.Context, lbARN string, marker *string) ([]elbv2types.Listener, *string, error) {
			out, err := client.DescribeListeners(ctx, &elasticloadbalancingv2.DescribeListenersInput{LoadBalancerArn: &lbARN, Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			fetchItemTags(ctx, bulk, out.Listeners, func(l elbv2types.Listener) *string { return l.ListenerArn })
			return out.Listeners, out.NextMarker, nil
		},
		func(lbARN string, l elbv2types.Listener) (*store.Resource, string, string) {
			return elbv2Row(acct, region, scanID, TypeELBv2Listener, "listeners", l.ListenerArn, &lbARN, l)
		},
		nil,
		func(rows []*store.Resource) {
			for _, r := range rows {
				listeners = append(listeners, childParent{id: r.NativeID, arn: r.NativeID})
			}
		},
		tags, p)
	return listeners, failed
}

// scanELBv2ListenerRules stores the rules of each listener stored this scan,
// with their tags, a page at a time. A rule does not name its listener, so
// the listener's ARN is stored beside it.
func scanELBv2ListenerRules(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, listeners []childParent, bulk *bulkRowTags, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "elasticloadbalancing:DescribeRules", []string{"ListenerNotFound"}, listeners,
		func(ctx context.Context, listenerARN string, marker *string) ([]elbv2types.Rule, *string, error) {
			out, err := client.DescribeRules(ctx, &elasticloadbalancingv2.DescribeRulesInput{ListenerArn: &listenerARN, Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			fetchItemTags(ctx, bulk, out.Rules, func(rule elbv2types.Rule) *string { return rule.RuleArn })
			return out.Rules, out.NextMarker, nil
		},
		func(listenerARN string, rule elbv2types.Rule) (*store.Resource, string, string) {
			return elbv2Row(acct, region, scanID, TypeELBv2ListenerRule, "listener rules", rule.RuleArn, &listenerARN,
				map[string]any{"rule": rule, "listenerArn": listenerARN})
		},
		tags, p)
}

// scanELBv2ListenerCertificates stores the certificates of each listener
// stored this scan. A listener certificate has no ARN of its own; its
// NativeID is the listener's ARN + ":cert/" + CertificateArn, and one without
// a CertificateArn is not stored.
func scanELBv2ListenerCertificates(ctx context.Context, client elbv2API, acct *account, region string, st *store.Store, scanID string, listeners []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "elasticloadbalancing:DescribeListenerCertificates", []string{"ListenerNotFound"}, childParentIDs(listeners),
		func(ctx context.Context, listenerARN string, marker *string) ([]elbv2types.Certificate, *string, error) {
			out, err := client.DescribeListenerCertificates(ctx, &elasticloadbalancingv2.DescribeListenerCertificatesInput{ListenerArn: &listenerARN, Marker: marker, PageSize: aws.Int32(elbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Certificates, out.NextMarker, nil
		},
		func(listenerARN string, cert elbv2types.Certificate) (*store.Resource, string, string) {
			if sv(cert.CertificateArn) == "" {
				return nil, "listener certificates have no CertificateArn", listenerARN
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeELBv2ListenerCertificate, NativeID: listenerARN + ":cert/" + sv(cert.CertificateArn),
				Region: &region, AttributesJSON: mustJSON(map[string]any{"listenerArn": listenerARN, "cert": cert}), DiscoveredBy: scanID,
			}, "", ""
		})
}
