package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// InitializationData seeds the test contact's attributes, which can carry
	// caller numbers or tokens. It is one JSON-encoded string, not a map, so no
	// per-key classification can keep its harmless values: the whole leaf goes.
	registerType(restype.Descriptor{Type: TypeConnectTestCase, Service: "connect", Redact: []redact.Rule{{Path: "InitializationData", Mode: redact.RedactScalar}}})
	// ListAttachedFilesConfigurations returns the effective configuration for
	// every attachment scope, defaults included, so a row exists per instance
	// and scope whether or not anyone configured it. Still not Managed: the
	// customer can change it, and Managed rows are left out of default
	// `disco check` input.
	registerType(restype.Descriptor{Type: TypeConnectAttachedFilesConfiguration, Service: "connect", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeConnectHoursOfOperationOverride, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectUseCase, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectEvaluationFormVersion, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectWorkspacePage, Service: "connect"})
}

// connectChildrenAPI is the narrow surface for the children family: per-instance
// leaves the other families don't own, and children of rows those families store.
type connectChildrenAPI interface {
	ListTestCases(context.Context, *connect.ListTestCasesInput, ...func(*connect.Options)) (*connect.ListTestCasesOutput, error)
	DescribeTestCase(context.Context, *connect.DescribeTestCaseInput, ...func(*connect.Options)) (*connect.DescribeTestCaseOutput, error)
	ListAttachedFilesConfigurations(context.Context, *connect.ListAttachedFilesConfigurationsInput, ...func(*connect.Options)) (*connect.ListAttachedFilesConfigurationsOutput, error)
	ListHoursOfOperationOverrides(context.Context, *connect.ListHoursOfOperationOverridesInput, ...func(*connect.Options)) (*connect.ListHoursOfOperationOverridesOutput, error)
	ListUseCases(context.Context, *connect.ListUseCasesInput, ...func(*connect.Options)) (*connect.ListUseCasesOutput, error)
	ListEvaluationFormVersions(context.Context, *connect.ListEvaluationFormVersionsInput, ...func(*connect.Options)) (*connect.ListEvaluationFormVersionsOutput, error)
	ListWorkspacePages(context.Context, *connect.ListWorkspacePagesInput, ...func(*connect.Options)) (*connect.ListWorkspacePagesOutput, error)
}

// isConnectNotFound matches a parent deleted between the phase that listed it
// and the child call.
func isConnectNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanConnectChildren must run after every other Connect family has returned:
// the hours-override, use-case, evaluation-form-version and workspace-page
// phases read their parents from the rows those families stored this scan. The
// test-case and attached-files phases need only the instance list but run here
// too, because runScanners cancels its siblings on the first error and a child
// failure must not cost the families their rows.
func scanConnectChildren(ctx context.Context, client connectChildrenAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanConnectTestCases(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectAttachedFilesConfigurations(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectHoursOfOperationOverrides(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectUseCases(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectEvaluationFormVersions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectWorkspacePages(ctx, client, acct, region, st, scanID)
		},
	)
}

// scanConnectTestCases lists test-case ids per instance, then describes each:
// only DescribeTestCase carries the tags and the test content.
func scanConnectTestCases(ctx context.Context, client connectChildrenAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return connectListDescribe(ctx, st, acct, region, "connect:ListTestCases", "connect:DescribeTestCase", "connect test cases", instances,
		func(ctx context.Context, instID string, emit func(string, string)) error {
			pager := connect.NewListTestCasesPaginator(client, &connect.ListTestCasesInput{InstanceId: &instID},
				func(o *connect.ListTestCasesPaginatorOptions) { o.Limit = 1000 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				for _, s := range out.TestCaseSummaryList {
					arn, id := sv(s.Arn), sv(s.Id)
					if arn != "" && id != "" {
						emit(arn, id)
					}
				}
			}
			return nil
		},
		func(gctx context.Context, instID, _, id string) (*store.Resource, error) {
			out, derr := client.DescribeTestCase(gctx, &connect.DescribeTestCaseInput{InstanceId: &instID, TestCaseId: &id})
			if derr != nil {
				return nil, derr
			}
			tc := out.TestCase
			if tc == nil || sv(tc.Arn) == "" {
				return nil, nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConnectTestCase, NativeID: *tc.Arn,
				Name: tc.Name, Region: &region, Status: nonEmptyPtr(string(tc.Status)),
				TagsJSON: mapTagsJSON(tc.Tags), AttributesJSON: mustJSON(tc), DiscoveredBy: scanID,
			}, nil
		})
}

func scanConnectAttachedFilesConfigurations(ctx context.Context, client connectChildrenAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var parents []childParent
	for _, inst := range instances {
		if inst.Id != nil && inst.Arn != nil {
			parents = append(parents, childParent{id: *inst.Id, arn: *inst.Arn})
		}
	}
	return childFanOut(ctx, st, acct, region, "connect:ListAttachedFilesConfigurations", parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := connect.NewListAttachedFilesConfigurationsPaginator(client, &connect.ListAttachedFilesConfigurationsInput{InstanceId: &p.id},
				func(o *connect.ListAttachedFilesConfigurationsPaginatorOptions) { o.Limit = 100 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.AttachedFilesConfigurations {
					scope := string(c.AttachmentScope)
					if scope == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectAttachedFilesConfiguration, NativeID: p.arn + "/attached-files-configuration/" + scope,
						Name: &scope, Region: &region,
						AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

func scanConnectHoursOfOperationOverrides(ctx context.Context, client connectChildrenAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectScannedParents(st, acct, region, scanID, TypeConnectHoursOfOperation)
	if err != nil {
		return 0, 0, err
	}
	return childFanOut(ctx, st, acct, region, "connect:ListHoursOfOperationOverrides", parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			instID, hoursID := connectChildARNIDs(p.arn)
			var rows []*store.Resource
			pager := connect.NewListHoursOfOperationOverridesPaginator(client, &connect.ListHoursOfOperationOverridesInput{InstanceId: &instID, HoursOfOperationId: &hoursID},
				func(o *connect.ListHoursOfOperationOverridesPaginatorOptions) { o.Limit = 100 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, o := range out.HoursOfOperationOverrideList {
					id := sv(o.HoursOfOperationOverrideId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectHoursOfOperationOverride, NativeID: p.arn + "/override/" + id,
						Name: o.Name, Region: &region,
						AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

func scanConnectUseCases(ctx context.Context, client connectChildrenAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectScannedParents(st, acct, region, scanID, TypeConnectIntegrationAssociation)
	if err != nil {
		return 0, 0, err
	}
	return childFanOut(ctx, st, acct, region, "connect:ListUseCases", parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			instID, assocID := connectChildARNIDs(p.arn)
			var rows []*store.Resource
			pager := connect.NewListUseCasesPaginator(client, &connect.ListUseCasesInput{InstanceId: &instID, IntegrationAssociationId: &assocID},
				func(o *connect.ListUseCasesPaginatorOptions) { o.Limit = 100 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, u := range out.UseCaseSummaryList {
					arn := sv(u.UseCaseArn)
					if arn == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectUseCase, NativeID: arn,
						Name: nonEmptyPtr(string(u.UseCaseType)), Region: &region,
						AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

func scanConnectEvaluationFormVersions(ctx context.Context, client connectChildrenAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectScannedParents(st, acct, region, scanID, TypeConnectEvaluationForm)
	if err != nil {
		return 0, 0, err
	}
	return childFanOut(ctx, st, acct, region, "connect:ListEvaluationFormVersions", parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			instID, formID := connectChildARNIDs(p.arn)
			var rows []*store.Resource
			pager := connect.NewListEvaluationFormVersionsPaginator(client, &connect.ListEvaluationFormVersionsInput{InstanceId: &instID, EvaluationFormId: &formID},
				func(o *connect.ListEvaluationFormVersionsPaginatorOptions) { o.Limit = 100 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, v := range out.EvaluationFormVersionSummaryList {
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectEvaluationFormVersion, NativeID: p.arn + "/version/" + strconv.Itoa(int(v.EvaluationFormVersion)),
						Region: &region, Status: nonEmptyPtr(string(v.Status)), CreatedAt: tp(v.CreatedTime),
						AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

func scanConnectWorkspacePages(ctx context.Context, client connectChildrenAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectScannedParents(st, acct, region, scanID, TypeConnectWorkspace)
	if err != nil {
		return 0, 0, err
	}
	return childFanOut(ctx, st, acct, region, "connect:ListWorkspacePages", parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			instID, workspaceID := connectChildARNIDs(p.arn)
			var rows []*store.Resource
			pager := connect.NewListWorkspacePagesPaginator(client, &connect.ListWorkspacePagesInput{InstanceId: &instID, WorkspaceId: &workspaceID},
				func(o *connect.ListWorkspacePagesPaginatorOptions) { o.Limit = 100 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, pg := range out.WorkspacePageList {
					page := sv(pg.Page)
					if page == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectWorkspacePage, NativeID: p.arn + "/page/" + page,
						Name: &page, Region: &region,
						AttributesJSON: mustJSON(pg), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// errConnectParentARN marks a stored Connect parent whose NativeID lacks the
// shape its own scanner writes.
var errConnectParentARN = errors.New("connect parent NativeID does not have the shape its scanner writes")

// connectScannedParents returns the parentType rows this scan stored in region,
// for a parent whose NativeID is `instance/{id}/{kind}/{id}`.
func connectScannedParents(st *store.Store, acct *account, region, scanID, parentType string) ([]childParent, error) {
	return connectStoredParents(st, acct, region, scanID, parentType, func(arn string) string {
		_, id := connectChildARNIDs(arn)
		return id
	})
}

// connectStoredParents returns the parentType rows this scan stored in region,
// each keyed by the id idOf reads from its NativeID; a row idOf yields no id
// for is an error.
func connectStoredParents(st *store.Store, acct *account, region, scanID, parentType string, idOf func(arn string) string) ([]childParent, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{parentType},
		Regions: []string{region}, SeenBy: scanID, Limit: util.AllResources,
	})
	if err != nil {
		return nil, fmt.Errorf("list %s parents: %w", parentType, err)
	}
	var parents []childParent
	for _, r := range rows {
		id := idOf(r.NativeID)
		if id == "" {
			return nil, fmt.Errorf("%w: %s %q", errConnectParentARN, parentType, r.NativeID)
		}
		parents = append(parents, childParent{id: id, arn: r.NativeID})
	}
	return parents, nil
}

// connectChildARNIDs splits `arn:…:instance/{instanceID}/{kind}/{id}` into its
// instance id and child id; both are "" for any other shape.
func connectChildARNIDs(arn string) (instanceID, id string) {
	_, path, ok := strings.Cut(arn, ":instance/")
	if !ok {
		return "", ""
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", ""
	}
	return parts[0], parts[2]
}
