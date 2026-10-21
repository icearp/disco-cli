package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2IPAMPrefixListResolver, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMPrefixListResolverTarget, Service: "ec2"})
}

const (
	ec2IPAMPrefixListResolversOp     = "ec2:DescribeIpamPrefixListResolvers"
	ec2IPAMPrefixListResolverRulesOp = "ec2:GetIpamPrefixListResolverRules"
	ec2IPAMPrefixListResolverPage    = 1000 // IpamMaxResults maximum, for both ops
)

// ipamPrefixListResolverAttrs is a resolver as DescribeIpamPrefixListResolvers
// returns it, with its rules beside the SDK fields. Embedding keeps the SDK
// fields at the top level, where they were before Rules was added.
type ipamPrefixListResolverAttrs struct {
	ec2types.IpamPrefixListResolver
	Rules []ec2types.IpamPrefixListResolverRule
}

// scanIPAMPrefixListResolvers stores the IPAM prefix list resolvers, each
// with its CIDR selection rules embedded under Rules. A resolver whose rules
// cannot be read is never stored with an empty or missing Rules, which would
// read as "has no rules" and flip on every scan the read fails:
//   - denied or still transient after retries: the resolver is not stored,
//     with one warning per cause counting the resolvers skipped;
//   - any other error: the resolver is not stored and the phase fails, after
//     the other resolvers are stored. EC2's Smithy model lists no error for
//     the op, so a not-found for a resolver deleted since it was listed is not
//     known to be one and is not skipped.
//
// A delete-complete resolver is stored without Rules and its rules are not
// read: it no longer selects CIDRs, and whether EC2 still answers for it is
// unverified. A resolver listed without the ARN its NativeID is, or the id
// the rules read takes, is not stored and is warned.
func scanIPAMPrefixListResolvers(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	resolvers, err := listIPAMPrefixListResolvers(ctx, client, acct, region, st)
	if err != nil {
		return 0, 0, err
	}
	var (
		deleted     []*store.Resource
		ids         []string
		byID        = map[string]ec2types.IpamPrefixListResolver{}
		noARN, noID skipTally
	)
	for _, r := range resolvers {
		arn, id := sv(r.IpamPrefixListResolverArn), sv(r.IpamPrefixListResolverId)
		switch {
		case arn == "":
			noARN.skipped(id, nil)
		case r.State == ec2types.IpamPrefixListResolverStateDeleteComplete:
			deleted = append(deleted, ipamPrefixListResolverRow(acct, region, scanID, r, mustJSON(r)))
		case id == "":
			noID.skipped(arn, nil)
		default:
			ids = append(ids, id)
			byID[id] = r
		}
	}
	warnIPAMPrefixListResolversNotStored(st, acct, region, &noARN, "listed without IpamPrefixListResolverArn, the NativeID")
	warnIPAMPrefixListResolversNotStored(st, acct, region, &noID, "listed without IpamPrefixListResolverId, so their rules cannot be read")
	var (
		transient transientTally
		denied    skipTally
		mu        sync.Mutex
		failed    = map[string]error{}
	)
	// A hard error is collected rather than returned to describeFanOut, which
	// would cancel the other resolvers' reads and discard the rows read so far.
	rows, err := describeFanOut(ctx, st, acct, region, ec2IPAMPrefixListResolverRulesOp, ids, neverSkip,
		func(ctx context.Context, id string) (*store.Resource, error) {
			rules, err := listIPAMPrefixListResolverRules(ctx, client, id)
			switch {
			case err == nil:
				attrs := ipamPrefixListResolverAttrs{IpamPrefixListResolver: byID[id], Rules: rules}
				return ipamPrefixListResolverRow(acct, region, scanID, byID[id], mustJSON(attrs)), nil
			case transient.drop(id, err):
			case isAccessDenied(err):
				denied.skipped(id, err)
			default:
				mu.Lock()
				failed[id] = err
				mu.Unlock()
			}
			return nil, nil
		})
	transient.warnSkipped(st, ec2IPAMPrefixListResolverRulesOp, acct, region)
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, ec2IPAMPrefixListResolverRulesOp, acct.ID, region,
			fmt.Errorf("%d IPAM prefix list resolver(s) not stored, first %s: %w", denied.count, denied.first, denied.err))
	}
	errs := []error{err}
	for _, id := range slices.Sorted(maps.Keys(failed)) {
		errs = append(errs, fmt.Errorf("%s %s: %w", ec2IPAMPrefixListResolverRulesOp, id, failed[id]))
	}
	total, inserted, uerr := upsertBatch(st, append(deleted, rows...), "ec2 ipam prefix list resolvers")
	return total, inserted, errors.Join(append(errs, uerr)...)
}

// warnIPAMPrefixListResolversNotStored warns once for the resolvers t
// counted, saying why they were not stored and naming the first.
func warnIPAMPrefixListResolversNotStored(st *store.Store, acct *account, region string, t *skipTally, why string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: ec2IPAMPrefixListResolversOp, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d IPAM prefix list resolver(s) not stored: %s; first %q", t.count, why, t.first),
	})
}

// listIPAMPrefixListResolvers returns the resolvers in region. A denial is
// warned and keeps the resolvers of the pages before it; a feature gap on the
// first page means the Region has none.
func listIPAMPrefixListResolvers(ctx context.Context, client ec2API, acct *account, region string, st *store.Store) ([]ec2types.IpamPrefixListResolver, error) {
	var resolvers []ec2types.IpamPrefixListResolver
	pager := ec2.NewDescribeIpamPrefixListResolversPaginator(client, &ec2.DescribeIpamPrefixListResolversInput{},
		func(o *ec2.DescribeIpamPrefixListResolversPaginatorOptions) { o.Limit = ec2IPAMPrefixListResolverPage })
	for first := true; pager.HasMorePages(); first = false {
		page, err := pager.NextPage(ctx)
		switch {
		case err == nil:
			resolvers = append(resolvers, page.IpamPrefixListResolvers...)
		case isAccessDenied(err):
			return resolvers, skipIfAccessDenied(st, ec2IPAMPrefixListResolversOp, acct.ID, region, err)
		case first && isEC2RegionFeatureGap(err):
			return nil, nil
		default:
			return nil, fmt.Errorf("%s: %w", ec2IPAMPrefixListResolversOp, err)
		}
	}
	return resolvers, nil
}

// listIPAMPrefixListResolverRules returns every rule of one resolver, sorted,
// and never nil, so a resolver with no rules stores Rules as []. It loops by
// hand rather than through the SDK paginator, which can only stop silently on
// a repeated token; a repeated token fails the read instead.
func listIPAMPrefixListResolverRules(ctx context.Context, client ec2API, id string) ([]ec2types.IpamPrefixListResolverRule, error) {
	rules := []ec2types.IpamPrefixListResolverRule{}
	in := &ec2.GetIpamPrefixListResolverRulesInput{
		IpamPrefixListResolverId: &id,
		MaxResults:               sdkaws.Int32(ec2IPAMPrefixListResolverPage),
	}
	for {
		out, err := client.GetIpamPrefixListResolverRules(ctx, in)
		if err != nil {
			return nil, err
		}
		rules = append(rules, out.Rules...)
		next := sv(out.NextToken)
		if next == "" {
			break
		}
		if next == sv(in.NextToken) {
			return nil, fmt.Errorf("NextToken %q: %w", next, errRepeatedNextToken)
		}
		in.NextToken = out.NextToken
	}
	// Neither a rule nor a condition has an id, and any field can be the only
	// difference between two of them, so each sorts by its whole encoding:
	// the order is total and unchanged rules never reorder the row. A CIDR
	// must match every condition of a rule (the SDK doc for
	// RequestIpamPrefixListResolverRuleCondition), so the conditions' order
	// selects nothing. They are sorted first because they are part of the
	// rule's key.
	for i := range rules {
		slices.SortFunc(rules[i].Conditions, byEncoding)
	}
	slices.SortFunc(rules, byEncoding)
	return rules, nil
}

// byEncoding orders two values by their JSON encodings.
func byEncoding[T any](a, b T) int { return cmp.Compare(mustJSON(a), mustJSON(b)) }

func ipamPrefixListResolverRow(acct *account, region, scanID string, r ec2types.IpamPrefixListResolver, attrs string) *store.Resource {
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeEC2IPAMPrefixListResolver,
		NativeID:       sv(r.IpamPrefixListResolverArn),
		Region:         &region,
		TagsJSON:       awsTagsJSON(r.Tags),
		AttributesJSON: attrs,
		DiscoveredBy:   scanID,
	}
}

func scanIPAMPrefixListResolverTargets(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpamPrefixListResolverTargets", acct, region, st,
		ec2.NewDescribeIpamPrefixListResolverTargetsPaginator(client, &ec2.DescribeIpamPrefixListResolverTargetsInput{}),
		func(page *ec2.DescribeIpamPrefixListResolverTargetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, t := range page.IpamPrefixListResolverTargets {
				arn := sv(t.IpamPrefixListResolverTargetArn)
				if arn == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMPrefixListResolverTarget,
					NativeID:       arn,
					Region:         &region,
					TagsJSON:       awsTagsJSON(t.Tags),
					AttributesJSON: mustJSON(t),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
