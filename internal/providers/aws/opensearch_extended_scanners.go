package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/icearp/disco-cli/store"
)

// scanOpenSearchApplications stores the OpenSearch applications with their
// tags (tags), a page at a time, and returns the stored ones as
// data-source-attachment parents, keyed by Id. It reports whether a write
// failed, after which the scan must stop.
func scanOpenSearchApplications(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, osListApplicationsOp, "applications", "Id",
		func(ctx context.Context, token *string) ([]opensearchtypes.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &opensearch.ListApplicationsInput{MaxResults: openSearchPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ApplicationSummaries, out.NextToken, nil
		},
		func(a opensearchtypes.ApplicationSummary) (*store.Resource, string, string) {
			return arnRow("applications", a.Arn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchApplication, NativeID: sv(a.Arn),
				Name: a.Name, Region: &region, Status: nonEmptyPtr(string(a.Status)),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		func(a opensearchtypes.ApplicationSummary) string { return sv(a.Id) },
		tags, p)
}

// errOpenSearchOpNotDeployed marks an app whose first attachment call the
// region's endpoint does not serve.
var errOpenSearchOpNotDeployed = errors.New("opensearch:ListDataSourceAttachments not served in this region")

// scanOpenSearchDataSourceAttachments lists each application's data source
// attachments (domains and serverless collections attached to it, in every
// state). An attachment has no ARN, so its NativeID is
// `{applicationARN}/data-source-attachment/{AttachmentId}`. An application
// deleted since it was listed (ResourceNotFoundException, on any page) is
// skipped silently. The op is newer than the application APIs (SDK v1.72.0,
// 2026-06), so an app's first call failing as isOpNotDeployed is also a silent
// skip; on a later page that error is returned.
func scanOpenSearchDataSourceAttachments(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, error) {
	isSkip := func(err error) bool {
		return errors.Is(err, errOpenSearchOpNotDeployed) || isAPIErrorCode(err, "ResourceNotFoundException")
	}
	return childFanOut(ctx, st, acct, region, "opensearch:ListDataSourceAttachments", apps, isSkip,
		func(ctx context.Context, app childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			in := &opensearch.ListDataSourceAttachmentsInput{Id: &app.id}
			for {
				out, err := client.ListDataSourceAttachments(ctx, in)
				if err != nil {
					if in.NextToken == nil && isOpNotDeployed(err) {
						return nil, errOpenSearchOpNotDeployed
					}
					return nil, err
				}
				for _, a := range out.Attachments {
					id := sv(a.AttachmentId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeOpenSearchDataSourceAttachment, NativeID: app.arn + "/data-source-attachment/" + id,
						Name: a.AttachmentId, Region: &region, Status: nonEmptyPtr(string(a.Status)),
						AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
				if done, err := lastPage(in.NextToken, out.NextToken); done {
					return rows, err
				}
				in.NextToken = out.NextToken
			}
		})
}
