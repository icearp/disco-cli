package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/securityir"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityIRCase, Service: "security-ir", Upstream: "AWS::security-ir::case", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityIRMembership, Service: "security-ir", Upstream: "AWS::security-ir::membership", Leaf: true})
	registerService(serviceEntry{
		name: "aws:security-ir",
		fn:   scanSecurityIR,
	})
}

type securityIRAPI interface {
	ListCases(context.Context, *securityir.ListCasesInput, ...func(*securityir.Options)) (*securityir.ListCasesOutput, error)
	ListMemberships(context.Context, *securityir.ListMembershipsInput, ...func(*securityir.Options)) (*securityir.ListMembershipsOutput, error)
}

// scanSecurityIR discovers Security Incident Response cases and memberships.
func scanSecurityIR(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := securityir.NewFromConfig(acct.cfg, func(o *securityir.Options) { o.Region = region })

	t, i, ferr := scanSecurityIRCases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSecurityIRMemberships(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSecurityIRCases(ctx context.Context, client securityIRAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityir.NewListCasesPaginator(client, &securityir.ListCasesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// Account not signed up for Security IR — whole service is inert,
			// so mark disabled (progress line reads "(account: disabled)")
			// rather than surfacing a scan error.
			if isAPIErrorCode(err, "SecurityIncidentResponseNotActiveException") {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "security-ir:ListCases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("security-ir:ListCases: %w", err)
		}
		for _, c := range out.Items {
			id := sv(c.CaseId)
			if id == "" {
				continue
			}
			// ListCases may omit CaseArn; synthesize the canonical ARN from
			// CaseId for a stable NativeID across re-scans.
			arn := sv(c.CaseArn)
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:security-ir:%s:%s:case/%s", region, acct.ID, id)
			}
			status := string(c.CaseStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityIRCase, NativeID: arn,
				Name: c.Title, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "security-ir cases")
}

func scanSecurityIRMemberships(ctx context.Context, client securityIRAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityir.NewListMembershipsPaginator(client, &securityir.ListMembershipsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "security-ir:ListMemberships", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("security-ir:ListMemberships: %w", err)
		}
		for _, m := range out.Items {
			id := sv(m.MembershipId)
			if id == "" {
				continue
			}
			arn := sv(m.MembershipArn)
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:security-ir:%s:%s:membership/%s", region, acct.ID, id)
			}
			status := string(m.MembershipStatus)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityIRMembership, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "security-ir memberships")
}
