package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/icearp/disco-cli/store"
)

// eksChildAPI lists the ops for the children of access entries and clusters.
type eksChildAPI interface {
	ListAssociatedAccessPolicies(context.Context, *eks.ListAssociatedAccessPoliciesInput, ...func(*eks.Options)) (*eks.ListAssociatedAccessPoliciesOutput, error)
	ListCertificateAuthorities(context.Context, *eks.ListCertificateAuthoritiesInput, ...func(*eks.Options)) (*eks.ListCertificateAuthoritiesOutput, error)
	DescribeCertificateAuthority(context.Context, *eks.DescribeCertificateAuthorityInput, ...func(*eks.Options)) (*eks.DescribeCertificateAuthorityOutput, error)
}

// eksAccessEntryRef is an access entry stored this scan: the two parts
// ListAssociatedAccessPolicies keys on, and the row's NativeID.
type eksAccessEntryRef struct{ cluster, principalARN, nativeID string }

// isEKSNotFound reports a parent deleted between its listing and the child call.
func isEKSNotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// eksAssociatedAccessPolicyNativeID builds the NativeID of one policy
// association. The kind segment differs from access-entry/ on purpose: an IAM
// path may contain "/" and ":", so {accessEntryNativeID}/access-policy/{policyArn}
// could equal the NativeID of an access entry whose principal path spells that
// suffix. Within the type the id stays unique because a cluster name holds no
// "/" and an access-policy ARN holds exactly one, so the parts split back apart
// from the right.
func eksAssociatedAccessPolicyNativeID(region, acct string, entry eksAccessEntryRef, policyARN string) string {
	return eksChildARN(region, acct, "associated-access-policy", entry.cluster, entry.principalARN+"/"+policyARN)
}

// scanEKSAssociatedAccessPolicies lists the access policies associated with
// each access entry stored this scan. The policies themselves are AWS-owned
// catalogue items disco does not scan, so the association has no edge to them.
func scanEKSAssociatedAccessPolicies(ctx context.Context, client eksChildAPI, acct *account, region string, st *store.Store, scanID string, entries []eksAccessEntryRef) (int, int, error) {
	parents := make([]childParent, 0, len(entries))
	byID := make(map[string]eksAccessEntryRef, len(entries))
	for _, e := range entries {
		parents = append(parents, childParent{id: e.nativeID, arn: e.nativeID})
		byID[e.nativeID] = e
	}
	return childFanOut(ctx, st, acct, region, "eks:ListAssociatedAccessPolicies", parents, isEKSNotFound,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return listEKSAssociatedAccessPolicies(ctx, client, acct, region, scanID, byID[p.id])
		})
}

func listEKSAssociatedAccessPolicies(ctx context.Context, client eksChildAPI, acct *account, region, scanID string, entry eksAccessEntryRef) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := eks.NewListAssociatedAccessPoliciesPaginator(client, &eks.ListAssociatedAccessPoliciesInput{
		ClusterName: &entry.cluster, PrincipalArn: &entry.principalARN,
	})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range out.AssociatedAccessPolicies {
			policyARN := sv(p.PolicyArn)
			if policyARN == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeEKSAssociatedAccessPolicy,
				NativeID: eksAssociatedAccessPolicyNativeID(region, acct.ID, entry, policyARN),
				Name:     p.PolicyArn, Region: &region, CreatedAt: tp(p.AssociatedAt),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

// scanEKSCertificateAuthorities lists each cluster's certificate authorities
// (the signing CA and, during a rotation, its successor) and describes each
// one: only the full CA carries its validity period and public certificate.
// A denied or vanished describe drops that CA alone; the denial warns once.
func scanEKSCertificateAuthorities(ctx context.Context, client eksChildAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent) (int, int, error) {
	describeErrs := &fanOutErrs{st: st, acct: acct, region: region, op: "eks:DescribeCertificateAuthority", isSkip: isEKSNotFound}
	return childFanOut(ctx, st, acct, region, "eks:ListCertificateAuthorities", clusters, isEKSNotFound,
		func(ctx context.Context, c childParent) ([]*store.Resource, error) {
			return listEKSCertificateAuthorities(ctx, client, acct, region, scanID, c.id, describeErrs)
		})
}

func listEKSCertificateAuthorities(ctx context.Context, client eksChildAPI, acct *account, region, scanID, cluster string, describeErrs *fanOutErrs) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := eks.NewListCertificateAuthoritiesPaginator(client, &eks.ListCertificateAuthoritiesInput{ClusterName: &cluster})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range out.CertificateAuthorities {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			desc, err := client.DescribeCertificateAuthority(ctx, &eks.DescribeCertificateAuthorityInput{ClusterName: &cluster, CertificateAuthorityId: &id})
			if err != nil {
				if kerr := describeErrs.keyErr(cluster+"/"+id, err); kerr != nil {
					return nil, kerr
				}
				continue
			}
			ca := desc.CertificateAuthority
			if ca == nil {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeEKSCertificateAuthority,
				NativeID: eksChildARN(region, acct.ID, "certificate-authority", cluster, id),
				Name:     &id, Region: &region, CreatedAt: tp(ca.CreatedAt), Status: nonEmptyPtr(string(ca.SigningStatus)),
				AttributesJSON: mustJSON(ca), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
