package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/mpa"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMPAApprovalTeam, Service: "mpa"})
	registerType(restype.Descriptor{Type: TypeMPAIdentitySource, Service: "mpa"})
	registerType(restype.Descriptor{Type: TypeMPAResourcePolicy, Service: "mpa"})
	registerService(serviceEntry{
		name: "aws:mpa",
		fn:   scanMPA,
	})
}

type mpaAPI interface {
	ListApprovalTeams(context.Context, *mpa.ListApprovalTeamsInput, ...func(*mpa.Options)) (*mpa.ListApprovalTeamsOutput, error)
	ListIdentitySources(context.Context, *mpa.ListIdentitySourcesInput, ...func(*mpa.Options)) (*mpa.ListIdentitySourcesOutput, error)
	ListResourcePolicies(context.Context, *mpa.ListResourcePoliciesInput, ...func(*mpa.Options)) (*mpa.ListResourcePoliciesOutput, error)
}

// scanMPA discovers Multi-Party Approval (MPA) approval teams, identity
// sources and the resource policies of each approval team. Not scanned:
// policies and policy versions (AWS-provided definitions that approval teams
// reference by ARN; the SDK has no op to create one) and approval sessions
// (ephemeral).
//
// The resource policy phase runs after both existing phases, so its failure
// cannot cost their rows.
func scanMPA(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mpa.NewFromConfig(acct.cfg, func(o *mpa.Options) { o.Region = region })

	var teams []childParent
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			teams, t, i, err = scanMPAApprovalTeams(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanMPAIdentitySources(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMPAResourcePolicies(ctx, client, acct, region, st, scanID, teams) },
	} {
		t, i, ferr := phase()
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

// scanMPAApprovalTeams stores the approval teams and returns them as resource
// policy fan-out parents.
func scanMPAApprovalTeams(ctx context.Context, client mpaAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var batch []*store.Resource
	var teams []childParent
	var nextToken *string
	for {
		out, err := client.ListApprovalTeams(ctx, &mpa.ListApprovalTeamsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mpa:ListApprovalTeams", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mpa:ListApprovalTeams: %w", err)
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
			teams = append(teams, childParent{id: arn, arn: arn})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "mpa approval-teams")
	return teams, total, inserted, err
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

// scanMPAResourcePolicies lists the resource policies of each given approval
// team. The policy document (GetResourcePolicy) is not fetched.
//
// That approval teams are the resources ListResourcePolicies takes is
// inferred, not documented: the SDK types ResourceArn as a plain string and
// binds no resource to the op. The evidence is that the response's PolicyType
// enum is shared with the catalogue Policy shapes, which the SDK documents as
// "permissions for team resources". So a ValidationException (the op's modeled
// bad-input error) warns once that the binding is unverified and ends the
// phase, rather than failing the scan or being skipped silently.
//
// The NativeID is {teamARN}/resource-policy/{type}/{name}: GetResourcePolicy
// keys a policy on that triple, and the returned PolicyArn is not documented
// as unique per team.
func scanMPAResourcePolicies(ctx context.Context, client mpaAPI, acct *account, region string, st *store.Store, scanID string, teams []childParent) (int, int, error) {
	const op = "mpa:ListResourcePolicies"
	total, inserted, err := childFanOut(ctx, st, acct, region, op, teams, isMPANotFound,
		func(ctx context.Context, team childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := mpa.NewListResourcePoliciesPaginator(client, &mpa.ListResourcePoliciesInput{ResourceArn: &team.arn})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, p := range out.ResourcePolicies {
					name := sv(p.PolicyName)
					if name == "" || p.PolicyType == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeMPAResourcePolicy,
						NativeID: team.arn + "/resource-policy/" + string(p.PolicyType) + "/" + name,
						Name:     &name, Region: &region,
						AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
	if isAPIErrorCode(err, "ValidationException") {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: "approval team ARN rejected as ResourceArn (the binding is unverified); resource policies not scanned: " + err.Error(),
		})
		return 0, 0, nil
	}
	return total, inserted, err
}

// isMPANotFound matches an approval team deleted between the phase that
// listed it and the call listing its resource policies.
func isMPANotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}
