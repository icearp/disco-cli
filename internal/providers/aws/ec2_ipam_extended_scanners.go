package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2ByoipCidr, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IpamByoasn, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IpamPolicyAllocationRule, Service: "ec2"})
}

const (
	ec2ByoipCidrsOp                  = "ec2:DescribeByoipCidrs"
	ec2IpamByoasnOp                  = "ec2:DescribeIpamByoasn"
	ec2IpamPolicyAllocationRulesOp   = "ec2:GetIpamPolicyAllocationRules"
	ec2ByoipCidrsPageLimit           = 100  // DescribeByoipCidrsMaxResults maximum
	ec2IpamByoasnPageLimit           = 100  // DescribeIpamByoasnMaxResults maximum
	ec2IpamPolicyAllocationPageLimit = 1000 // IpamMaxResults maximum
	// ec2IpamPolicyNotFound is the code EC2 is expected to return for a policy
	// deleted between its listing and its allocation-rule call. Not observed
	// live, so a skip on it warns rather than passing silently.
	ec2IpamPolicyNotFound = "InvalidIpamPolicyId.NotFound"
)

// scanEC2IPAMExtended runs the BYOIP, BYOASN, IPAM policy allocation-rule
// and IPAM prefix list resolver phases. They are independent: each runs
// whatever the others return, has its transient error demoted to a warning
// (one transient member would make the dispatcher read the whole join as
// transient) and the rest are joined. The resolver phase is here, not among
// scanEC2's categories, because a failed rules read fails it, and there it
// would cancel every other category.
// homeIPAM reports whether scanIPAMs stored this scan an IPAM whose home
// Region is region; policies are the stored policies whose allocation rules
// can still be read.
func scanEC2IPAMExtended(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string, homeIPAM bool, policies []childParent) (total, inserted int, err error) {
	var errs []error
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{ec2ByoipCidrsOp, func() (int, int, error) { return scanEC2ByoipCidrs(ctx, client, acct, region, st, scanID) }},
		{ec2IpamByoasnOp, func() (int, int, error) {
			// AWS's BYOASN tutorial (vpc/latest/ipam/tutorials-byoasn.html)
			// has the steps run from the IPAM admin account in the IPAM's home
			// Region (paraphrased, not re-checked), and DescribeIpamByoasn
			// takes no IPAM filter. Whether other Regions return the same ASNs
			// or reject the call is unverified, so the call is made only in a
			// Region that is home to an IPAM stored this scan.
			if !homeIPAM {
				return 0, 0, nil
			}
			return scanEC2IpamByoasns(ctx, client, acct, region, st, scanID)
		}},
		{ec2IpamPolicyAllocationRulesOp, func() (int, int, error) {
			return scanEC2IpamPolicyAllocationRules(ctx, client, acct, region, st, scanID, policies)
		}},
		{ec2IPAMPrefixListResolversOp, func() (int, int, error) {
			return scanIPAMPrefixListResolvers(ctx, client, acct, region, st, scanID)
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	return total, inserted, errors.Join(errs...)
}

// scanEC2ByoipCidrs stores the address ranges provisioned to EC2 through
// BYOIP. AWS documents no ARN for a range; the NativeID is synthesized in
// ec2ARN shape from the CIDR, which is unique within an account and region.
// Deprovisioned ranges are no longer provisioned and are dropped.
func scanEC2ByoipCidrs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := ec2.NewDescribeByoipCidrsPaginator(client, &ec2.DescribeByoipCidrsInput{},
		func(o *ec2.DescribeByoipCidrsPaginatorOptions) { o.Limit = ec2ByoipCidrsPageLimit })
	return ec2PageScan(ctx, ec2ByoipCidrsOp, acct, region, st, pager,
		func(page *ec2.DescribeByoipCidrsOutput) []*store.Resource {
			var out []*store.Resource
			for _, c := range page.ByoipCidrs {
				cidr := sv(c.Cidr)
				if cidr == "" || c.State == ec2types.ByoipCidrStateDeprovisioned {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2ByoipCidr,
					NativeID:       ec2ARN(region, acct.ID, "byoip-cidr", cidr),
					Name:           c.Cidr,
					Region:         &region,
					Status:         nonEmptyPtr(string(c.State)),
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			return out
		})
}

// scanEC2IpamByoasns stores the ASNs provisioned to IPAM. AWS documents no ARN
// for one; the NativeID is synthesized in ec2ARN shape from the IPAM id and
// the ASN, as the IPAM pool CIDR NativeID is. Deprovisioned ASNs are dropped.
// DescribeIpamByoasn has no SDK paginator.
func scanEC2IpamByoasns(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var children []containedChild
	var token *string
	for {
		page, perr := client.DescribeIpamByoasn(ctx, &ec2.DescribeIpamByoasnInput{
			MaxResults: sdkaws.Int32(ec2IpamByoasnPageLimit),
			NextToken:  token,
		})
		if perr != nil {
			if token == nil && isEC2RegionFeatureGap(perr) {
				return 0, 0, nil
			}
			return upsertRegionChildren(st, acct, region, ec2IpamByoasnOp, children, perr)
		}
		for _, a := range page.Byoasns {
			asn, ipamID := sv(a.Asn), sv(a.IpamId)
			if asn == "" || ipamID == "" || a.State == ec2types.AsnStateDeprovisioned {
				continue
			}
			children = append(children, containedChild{row: &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2IpamByoasn,
				NativeID:       ec2ARN(region, acct.ID, "ipam-byoasn", ipamID+"/"+asn),
				Name:           a.Asn,
				Region:         &region,
				Status:         nonEmptyPtr(string(a.State)),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}})
		}
		next := sv(page.NextToken)
		if next == "" || next == sv(token) {
			break
		}
		token = page.NextToken
	}
	return upsertRegionChildren(st, acct, region, ec2IpamByoasnOp, children, nil)
}

// ipamPolicyAllocationRuleNativeID builds the NativeID of the allocation rules
// one IPAM policy sets for a resource type in a locale. AWS documents no ARN
// for them; the pair is the key ModifyIpamPolicyAllocationRules writes under.
func ipamPolicyAllocationRuleNativeID(policyARN, resourceType, locale string) string {
	return policyARN + "/allocation-rule/" + resourceType + "/" + locale
}

// scanEC2IpamPolicyAllocationRules stores, under each policy, one row per
// (resource type, locale) document of allocation rules the policy returns.
func scanEC2IpamPolicyAllocationRules(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string, policies []childParent) (int, int, error) {
	var gone skipTally
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, ec2IpamPolicyAllocationRulesOp, policies,
		func(error) bool { return false }, byParentID,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := listIpamPolicyAllocationRules(ctx, client, acct, region, scanID, p)
			if gone.dropCode(ec2IpamPolicyNotFound, p.id, err) {
				return nil, nil
			}
			return rows, err
		})
	gone.warnCode(st, ec2IpamPolicyAllocationRulesOp, acct, region, ec2IpamPolicyNotFound,
		"IPAM policies", "a policy deleted since it was listed")
	return total, inserted, err
}

// listIpamPolicyAllocationRules returns the allocation-rule rows of one
// policy. GetIpamPolicyAllocationRules has no SDK paginator. A document with
// an empty AllocationRules list is stored as returned; whether Get synthesizes
// default documents for resource types never configured is unverified.
func listIpamPolicyAllocationRules(ctx context.Context, client ec2API, acct *account, region, scanID string, p childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	var token *string
	for {
		page, err := client.GetIpamPolicyAllocationRules(ctx, &ec2.GetIpamPolicyAllocationRulesInput{
			IpamPolicyId: &p.id,
			MaxResults:   sdkaws.Int32(ec2IpamPolicyAllocationPageLimit),
			NextToken:    token,
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range page.IpamPolicyDocuments {
			resourceType, locale := string(doc.ResourceType), sv(doc.Locale)
			if resourceType == "" || locale == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2IpamPolicyAllocationRule,
				NativeID:       ipamPolicyAllocationRuleNativeID(p.arn, resourceType, locale),
				Region:         &region,
				AttributesJSON: mustJSON(doc),
				DiscoveredBy:   scanID,
			})
		}
		next := sv(page.NextToken)
		if next == "" || next == sv(token) {
			return rows, nil
		}
		token = page.NextToken
	}
}
