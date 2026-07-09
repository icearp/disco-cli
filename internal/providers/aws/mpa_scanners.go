package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/mpa"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMPAApprovalTeam, Service: "mpa", Upstream: "AWS::MPA::ApprovalTeam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMPAIdentitySource, Service: "mpa", Upstream: "AWS::MPA::IdentitySource", Leaf: true})
	registerService(serviceEntry{
		name: "aws:mpa",
		fn:   scanMPA,
	})
}

type mpaAPI interface {
	ListApprovalTeams(context.Context, *mpa.ListApprovalTeamsInput, ...func(*mpa.Options)) (*mpa.ListApprovalTeamsOutput, error)
	ListIdentitySources(context.Context, *mpa.ListIdentitySourcesInput, ...func(*mpa.Options)) (*mpa.ListIdentitySourcesOutput, error)
}

// scanMPA discovers Multi-Party Approval (MPA) approval teams and identity
// sources.
func scanMPA(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mpa.NewFromConfig(acct.cfg, func(o *mpa.Options) { o.Region = region })

	t, i, ferr := scanMPAApprovalTeams(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanMPAIdentitySources(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanMPAApprovalTeams(ctx context.Context, client mpaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListApprovalTeams(ctx, &mpa.ListApprovalTeamsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mpa:ListApprovalTeams", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mpa:ListApprovalTeams: %w", err)
		}
		for _, t := range out.ApprovalTeams {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			status := string(t.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMPAApprovalTeam, NativeID: arn,
				Name: t.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "mpa approval-teams")
}

func scanMPAIdentitySources(ctx context.Context, client mpaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListIdentitySources(ctx, &mpa.ListIdentitySourcesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mpa:ListIdentitySources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mpa:ListIdentitySources: %w", err)
		}
		for _, s := range out.IdentitySources {
			arn := sv(s.IdentitySourceArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMPAIdentitySource, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "mpa identity-sources")
}
