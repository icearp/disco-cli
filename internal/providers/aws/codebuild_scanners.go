package aws

import (
	"context"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeBuildFleet, Service: "code-build"})
	registerType(restype.Descriptor{Type: TypeCodeBuildProject, Service: "code-build", Redact: []redact.Rule{{Path: "Environment.EnvironmentVariables[*].Value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCodeBuildReportGroup, Service: "code-build"})
	registerType(restype.Descriptor{Type: TypeCodeBuildSourceCredential, Service: "code-build"})
	registerService(serviceEntry{
		name: "aws:code-build",
		fn:   scanCodeBuild,
	})
}

const (
	cbListFleetsOp            = "codebuild:ListFleets"
	cbBatchGetFleetsOp        = "codebuild:BatchGetFleets"
	cbListProjectsOp          = "codebuild:ListProjects"
	cbBatchGetProjectsOp      = "codebuild:BatchGetProjects"
	cbListReportGroupsOp      = "codebuild:ListReportGroups"
	cbBatchGetReportGroupsOp  = "codebuild:BatchGetReportGroups"
	cbListSourceCredentialsOp = "codebuild:ListSourceCredentials"
)

const (
	// cbPageMax is the MaxResults cap ListFleets and ListReportGroups model;
	// ListProjects takes no page size.
	cbPageMax int32 = 100
	// cbBatchMax is the most names or ARNs each BatchGet op here accepts.
	cbBatchMax = 100
)

type codeBuildAPI interface {
	ListFleets(context.Context, *codebuild.ListFleetsInput, ...func(*codebuild.Options)) (*codebuild.ListFleetsOutput, error)
	ListProjects(context.Context, *codebuild.ListProjectsInput, ...func(*codebuild.Options)) (*codebuild.ListProjectsOutput, error)
	ListReportGroups(context.Context, *codebuild.ListReportGroupsInput, ...func(*codebuild.Options)) (*codebuild.ListReportGroupsOutput, error)
	ListSourceCredentials(context.Context, *codebuild.ListSourceCredentialsInput, ...func(*codebuild.Options)) (*codebuild.ListSourceCredentialsOutput, error)
	BatchGetProjects(context.Context, *codebuild.BatchGetProjectsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetProjectsOutput, error)
	BatchGetFleets(context.Context, *codebuild.BatchGetFleetsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetFleetsOutput, error)
	BatchGetReportGroups(context.Context, *codebuild.BatchGetReportGroupsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetReportGroupsOutput, error)
}

// scanCodeBuild discovers CodeBuild fleets, projects, report groups and
// source credentials. The fleet, project and report-group listings return
// names or ARNs only, so each is followed by its BatchGet op, whose elements
// are stored. The phases are independent: only a store write failure stops
// the rest.
func scanCodeBuild(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := codebuild.NewFromConfig(acct.cfg, func(o *codebuild.Options) { o.Region = region })
	return scanCodeBuildAll(ctx, client, acct, region, st, scanID)
}

func scanCodeBuildAll(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var p phaseTotals
	for _, ph := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{cbListFleetsOp, func() (int, int, error) { return scanCBFleets(ctx, client, acct, region, st, scanID) }},
		{cbListProjectsOp, func() (int, int, error) { return scanCBProjects(ctx, client, acct, region, st, scanID) }},
		{cbListReportGroupsOp, func() (int, int, error) { return scanCBReportGroups(ctx, client, acct, region, st, scanID) }},
		{cbListSourceCredentialsOp, func() (int, int, error) { return scanCBSourceCredentials(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// cbTagKV reads a CodeBuild tag for kvTagsJSON.
func cbTagKV(t cbtypes.Tag) (key, value *string) { return t.Key, t.Value }

// cbBatchPhase is one CodeBuild listing whose op returns names or ARNs only,
// and the BatchGet op whose elements are stored.
type cbBatchPhase[Elem any] struct {
	listOp, getOp string
	// what names the listed items in warnings, e.g. "fleets".
	what string
	page func(ctx context.Context, token *string) ([]string, *string, error)
	// get returns the elements BatchGet found for keys and the keys it
	// reports not found.
	get func(ctx context.Context, keys []string) ([]Elem, []string, error)
	row func(Elem) (r *store.Resource, why, key string)
	// isGap reports a first-page list error that means the op is not served
	// in the region; nil when the op has none.
	isGap func(error) bool
}

// cbBatchRead is what one BatchGet call returned.
type cbBatchRead[Elem any] struct {
	found    []Elem
	notFound []string
	err      error
}

// storeCBBatchPhase lists every key, reads them through the BatchGet op
// cbBatchMax at a time, concurrently, and stores the elements. The keys read
// before a failed page are still read and stored, and the page's error is
// returned. A batch's failure does not stop the others: a denied batch is
// warned about once for the op, any other error is returned after the rows
// are stored. Listed keys the BatchGet op reports not found, and elements
// with no ARN, are warned about once per reason after a successful write.
func storeCBBatchPhase[Elem any](ctx context.Context, st *store.Store, acct *account, region string, b cbBatchPhase[Elem]) (int, int, error) {
	keys, listErr := readTokenPages(ctx, b.page)
	if len(keys) == 0 && b.isGap != nil && b.isGap(listErr) {
		return 0, 0, nil
	}
	batches := slices.Collect(slices.Chunk(keys, cbBatchMax))
	reads, stopped := readEachKey(ctx, batches, func(ctx context.Context, keys []string) cbBatchRead[Elem] {
		found, notFound, err := b.get(ctx, keys)
		return cbBatchRead[Elem]{found, notFound, err}
	})
	var (
		rows   []*store.Resource
		skips  notStoredSkips
		denied skipTally
		errs   []error
	)
	notFoundWhy := b.what + " were reported not found by " + b.getOp
	for i, read := range reads {
		first := batches[i][0]
		switch {
		case read.err == nil:
		case isAccessDenied(read.err):
			denied.skipped(first, read.err)
		default:
			errs = append(errs, fmt.Errorf("%s batch from %s: %w", b.getOp, first, read.err))
		}
		rows = buildListedRows(read.found, b.row, rows, &skips)
		for _, k := range read.notFound {
			skips.skipped(notFoundWhy, k)
		}
	}
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", b.getOp, stopped))
	}
	return storeReadRows(st, acct, region, b.listOp, rows, listErr, func() {
		skips.warn(st, b.getOp, acct, region)
		denied.warnDeniedIn(st, b.getOp, acct, region, "batch(es) of listed "+b.what+" not stored,")
	}, errs...)
}

// cbRow builds the row of a CodeBuild element stored under its ARN, or
// returns why it cannot be stored, keyed by its name, when the ARN is empty.
func cbRow(acct *account, region, scanID, rtype, what string, arn, name *string, label string, attrs any, tags *string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty Arn", sv(name)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(attrs), TagsJSON: tags, DiscoveredBy: scanID,
	}, "", ""
}

func scanCBFleets(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeCBBatchPhase(ctx, st, acct, region, cbBatchPhase[cbtypes.Fleet]{
		listOp: cbListFleetsOp, getOp: cbBatchGetFleetsOp, what: "fleets",
		page: func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListFleets(ctx, &codebuild.ListFleetsInput{MaxResults: sdkaws.Int32(cbPageMax), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Fleets, out.NextToken, nil
		},
		get: func(ctx context.Context, keys []string) ([]cbtypes.Fleet, []string, error) {
			out, err := client.BatchGetFleets(ctx, &codebuild.BatchGetFleetsInput{Names: keys})
			if err != nil {
				return nil, nil, err
			}
			return out.Fleets, out.FleetsNotFound, nil
		},
		row: func(f cbtypes.Fleet) (*store.Resource, string, string) {
			label := sv(f.Name)
			if label == "" {
				label = sv(f.Arn)
			}
			return cbRow(acct, region, scanID, TypeCodeBuildFleet, "fleets", f.Arn, f.Name, label, f, kvTagsJSON(f.Tags, cbTagKV))
		},
		// Fleets are not offered in every region; there ListFleets answers
		// InvalidInputException "Unknown operation".
		isGap: func(err error) bool { return isAPIErrorWithMessage(err, "InvalidInputException", "Unknown operation") },
	})
}

// scanCBProjects stores the BatchGetProjects body of each listed project:
// ServiceRole, VpcConfig, EncryptionKey, Artifacts and LogsConfig, which the
// resolver reads, are not on the name-only listing.
func scanCBProjects(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeCBBatchPhase(ctx, st, acct, region, cbBatchPhase[cbtypes.Project]{
		listOp: cbListProjectsOp, getOp: cbBatchGetProjectsOp, what: "projects",
		page: func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListProjects(ctx, &codebuild.ListProjectsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Projects, out.NextToken, nil
		},
		get: func(ctx context.Context, keys []string) ([]cbtypes.Project, []string, error) {
			out, err := client.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: keys})
			if err != nil {
				return nil, nil, err
			}
			return out.Projects, out.ProjectsNotFound, nil
		},
		row: func(p cbtypes.Project) (*store.Resource, string, string) {
			return cbRow(acct, region, scanID, TypeCodeBuildProject, "projects", p.Arn, p.Name, sv(p.Name), p, kvTagsJSON(p.Tags, cbTagKV))
		},
	})
}

func scanCBReportGroups(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeCBBatchPhase(ctx, st, acct, region, cbBatchPhase[cbtypes.ReportGroup]{
		listOp: cbListReportGroupsOp, getOp: cbBatchGetReportGroupsOp, what: "report groups",
		page: func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListReportGroups(ctx, &codebuild.ListReportGroupsInput{MaxResults: sdkaws.Int32(cbPageMax), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ReportGroups, out.NextToken, nil
		},
		get: func(ctx context.Context, keys []string) ([]cbtypes.ReportGroup, []string, error) {
			out, err := client.BatchGetReportGroups(ctx, &codebuild.BatchGetReportGroupsInput{ReportGroupArns: keys})
			if err != nil {
				return nil, nil, err
			}
			return out.ReportGroups, out.ReportGroupsNotFound, nil
		},
		row: func(g cbtypes.ReportGroup) (*store.Resource, string, string) {
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Arn)
			}
			return cbRow(acct, region, scanID, TypeCodeBuildReportGroup, "report groups", g.Arn, g.Name, label, g, kvTagsJSON(g.Tags, cbTagKV))
		},
	})
}

// scanCBSourceCredentials stores the source credentials; ListSourceCredentials
// is not paginated.
func scanCBSourceCredentials(ctx context.Context, client codeBuildAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, cbListSourceCredentialsOp,
		func(ctx context.Context, _ *string) ([]cbtypes.SourceCredentialsInfo, *string, error) {
			out, err := client.ListSourceCredentials(ctx, &codebuild.ListSourceCredentialsInput{})
			if err != nil {
				return nil, nil, err
			}
			return out.SourceCredentialsInfos, nil, nil
		},
		func(c cbtypes.SourceCredentialsInfo) (*store.Resource, string, string) {
			stype := string(c.ServerType)
			if sv(c.Arn) == "" {
				return nil, "source credentials have an empty Arn", stype
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeBuildSourceCredential, NativeID: *c.Arn,
				Name: &stype, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		})
}
