package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
)

func init() {
	registerService(serviceEntry{
		name: "aws:work-spaces",
		fn:   scanWorkSpaces,
		emits: []coverage.TypeDecl{
			{Service: "work-spaces", DiscoType: TypeWorkSpacesWorkspace, Leaf: true},
			{Service: "work-spaces", DiscoType: TypeWorkSpacesConnectionAlias, Leaf: true},
			{Service: "work-spaces", DiscoType: TypeWorkSpacesWorkspacesPool, Leaf: true},
		},
	})
}

type workSpacesAPI interface {
	DescribeWorkspaces(context.Context, *workspaces.DescribeWorkspacesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesOutput, error)
	DescribeConnectionAliases(context.Context, *workspaces.DescribeConnectionAliasesInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectionAliasesOutput, error)
	DescribeWorkspacesPools(context.Context, *workspaces.DescribeWorkspacesPoolsInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesPoolsOutput, error)
}

// scanWorkSpaces discovers WorkSpaces workspaces, connection aliases, and
// workspaces pools. Workspaces and aliases synth ARN from ID;
// WorkspacesPool exposes PoolArn natively.
func scanWorkSpaces(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workspaces.NewFromConfig(acct.cfg, func(o *workspaces.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanWSWorkspaces(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSConnectionAliases(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWorkspacesPools(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanWSWorkspaces(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspaces.NewDescribeWorkspacesPaginator(client, &workspaces.DescribeWorkspacesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspaces: %w", err)
		}
		for _, w := range out.Workspaces {
			id := sv(w.WorkspaceId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:workspace/%s", region, acct.ID, id)
			label := id
			status := string(w.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspace, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces")
}

func scanWSConnectionAliases(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeConnectionAliases(ctx, &workspaces.DescribeConnectionAliasesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeConnectionAliases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeConnectionAliases: %w", err)
		}
		for _, a := range out.ConnectionAliases {
			id := sv(a.AliasId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:connectionalias/%s", region, acct.ID, id)
			label := sv(a.ConnectionString)
			if label == "" {
				label = id
			}
			status := string(a.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesConnectionAlias, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces connection-aliases")
}

func scanWSWorkspacesPools(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeWorkspacesPools(ctx, &workspaces.DescribeWorkspacesPoolsInput{NextToken: nextToken})
		if err != nil {
			// WorkSpaces Pools is a per-region sub-feature; in regions where
			// it has not launched, AWS returns a canned AccessDeniedException
			// pointing at the workspaces-access-control docs URL — distinct
			// from a real IAM denial which carries the
			// "is not authorized to perform: <action>" SDK-formatted body.
			// Silent-skip on the canned shape; warn on real denials.
			if isAccessDeniedWithMessage(err, "workspaces-access-control.html") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspacesPools", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspacesPools: %w", err)
		}
		for _, p := range out.WorkspacesPools {
			arn := sv(p.PoolArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspacesPool, NativeID: arn,
				Name: p.PoolName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces workspaces-pools")
}
