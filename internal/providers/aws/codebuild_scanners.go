package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeBuildFleet, Service: "code-build"})
	registerType(restype.Descriptor{Type: TypeCodeBuildProject, Service: "code-build", Redact: []redact.Rule{{Path: "Environment.EnvironmentVariables[*].Value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCodeBuildReportGroup, Service: "code-build"})
	registerType(restype.Descriptor{Type: TypeCodeBuildSourceCredential, Service: "code-build", Leaf: true})
	registerService(serviceEntry{
		name: "aws:code-build",
		fn:   scanCodeBuild,
	})
}

type codeBuildAPI interface {
	ListFleets(context.Context, *codebuild.ListFleetsInput, ...func(*codebuild.Options)) (*codebuild.ListFleetsOutput, error)
	ListProjects(context.Context, *codebuild.ListProjectsInput, ...func(*codebuild.Options)) (*codebuild.ListProjectsOutput, error)
	ListReportGroups(context.Context, *codebuild.ListReportGroupsInput, ...func(*codebuild.Options)) (*codebuild.ListReportGroupsOutput, error)
	ListSourceCredentials(context.Context, *codebuild.ListSourceCredentialsInput, ...func(*codebuild.Options)) (*codebuild.ListSourceCredentialsOutput, error)
	BatchGetProjects(context.Context, *codebuild.BatchGetProjectsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetProjectsOutput, error)
	BatchGetFleets(context.Context, *codebuild.BatchGetFleetsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetFleetsOutput, error)
	BatchGetReportGroups(context.Context, *codebuild.BatchGetReportGroupsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetReportGroupsOutput, error)
}

// scanCodeBuild discovers CodeBuild fleets, projects, report groups, and
// source credentials. ListFleets/ListProjects return only IDs (ARNs in the
// case of fleets, names for projects); skip BatchGet enrichment for
// coverage and synthesize project ARNs from names.
func scanCodeBuild(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codebuild.NewFromConfig(acct.cfg, func(o *codebuild.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCBFleets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCBProjects(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCBReportGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCBSourceCredentials(ctx, client, acct, region, st, scanID) },
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

func scanCBFleets(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := codebuild.NewListFleetsPaginator(client, &codebuild.ListFleetsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// Op not deployed in this region.
			if isAPIErrorWithMessage(err, "InvalidInputException", "Unknown operation") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codebuild:ListFleets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codebuild:ListFleets: %w", err)
		}
		for _, fa := range out.Fleets {
			if fa != "" {
				arns = append(arns, fa)
			}
		}
	}
	var batch []*store.Resource
	for i := 0; i < len(arns); i += 100 {
		end := i + 100
		if end > len(arns) {
			end = len(arns)
		}
		out, err := client.BatchGetFleets(ctx, &codebuild.BatchGetFleetsInput{Names: arns[i:end]})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codebuild:BatchGetFleets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codebuild:BatchGetFleets: %w", err)
		}
		for _, f := range out.Fleets {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			label := sv(f.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeBuildFleet, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "codebuild fleets")
}

func scanCBProjects(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// List names first, then BatchGetProjects (max 100/call) for full bodies
	// — list returns names only; ServiceRole/VpcConfig/EncryptionKey/
	// Artifacts/LogsConfig refs live only on Project.
	pager := codebuild.NewListProjectsPaginator(client, &codebuild.ListProjectsInput{})
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codebuild:ListProjects", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codebuild:ListProjects: %w", err)
		}
		for _, n := range out.Projects {
			if n != "" {
				names = append(names, n)
			}
		}
	}
	var batch []*store.Resource
	for i := 0; i < len(names); i += 100 {
		end := i + 100
		if end > len(names) {
			end = len(names)
		}
		out, err := client.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: names[i:end]})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codebuild:BatchGetProjects", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codebuild:BatchGetProjects: %w", err)
		}
		for _, p := range out.Projects {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeBuildProject, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "codebuild projects")
}

func scanCBReportGroups(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := codebuild.NewListReportGroupsPaginator(client, &codebuild.ListReportGroupsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codebuild:ListReportGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codebuild:ListReportGroups: %w", err)
		}
		for _, arn := range out.ReportGroups {
			if arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	var batch []*store.Resource
	for i := 0; i < len(arns); i += 100 {
		end := i + 100
		if end > len(arns) {
			end = len(arns)
		}
		out, err := client.BatchGetReportGroups(ctx, &codebuild.BatchGetReportGroupsInput{ReportGroupArns: arns[i:end]})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codebuild:BatchGetReportGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codebuild:BatchGetReportGroups: %w", err)
		}
		for _, g := range out.ReportGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeBuildReportGroup, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "codebuild report-groups")
}

func scanCBSourceCredentials(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.ListSourceCredentials(ctx, &codebuild.ListSourceCredentialsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "codebuild:ListSourceCredentials", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("codebuild:ListSourceCredentials: %w", err)
	}
	var batch []*store.Resource
	for _, c := range out.SourceCredentialsInfos {
		arn := sv(c.Arn)
		if arn == "" {
			continue
		}
		stype := string(c.ServerType)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCodeBuildSourceCredential, NativeID: arn,
			Name: &stype, Region: &region,
			AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "codebuild source-credentials")
}
