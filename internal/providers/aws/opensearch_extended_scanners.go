package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/icearp/disco-cli/store"
)

// scanOpenSearchApplications discovers OpenSearch applications and returns
// them as data-source-attachment parents.
func scanOpenSearchApplications(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var (
		batch   []*store.Resource
		parents []childParent
	)
	var nextToken *string
	for {
		out, err := client.ListApplications(ctx, &opensearch.ListApplicationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "opensearch:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("opensearch:ListApplications: %w", err)
		}
		for _, a := range out.ApplicationSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			if id := sv(a.Id); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchApplication, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "opensearch applications")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, total, inserted, nil
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
func scanOpenSearchDataSourceAttachments(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
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
				if next := sv(out.NextToken); next == "" || next == sv(in.NextToken) {
					return rows, nil
				}
				in.NextToken = out.NextToken
			}
		})
}
