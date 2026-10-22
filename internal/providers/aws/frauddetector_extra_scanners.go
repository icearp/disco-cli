package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
	frauddetectortypes "github.com/aws/aws-sdk-go-v2/service/frauddetector/types"
	"github.com/icearp/disco-cli/store"
)

// fdSoftError tolerates the closed-to-new-customers + AccessDenied shapes the
// rest of the frauddetector scanner already handles per phase.
func fdSoftError(st *store.Store, op, acctID, region string, err error) bool {
	if isClosedToNewCustomers(err) {
		return true
	}
	if isAccessDenied(err) {
		_ = skipIfAccessDenied(st, op, acctID, region, err)
		return true
	}
	return false
}

// fdModel is a model listed by scanFDModels: DescribeModelVersions keys on
// both its id and its type.
type fdModel struct {
	ref       childParent
	modelType frauddetectortypes.ModelTypeEnum
}

func scanFDModels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) ([]fdModel, int, int, error) {
	input := &frauddetector.GetModelsInput{}
	var (
		batch  []*store.Resource
		models []fdModel
	)
	for {
		out, err := client.GetModels(ctx, input)
		if err != nil {
			if fdSoftError(st, "frauddetector:GetModels", acct.ID, region, err) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("frauddetector:GetModels: %w", err)
		}
		for _, m := range out.Models {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorModel, NativeID: arn,
				Name: m.ModelId, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
			if id := sv(m.ModelId); id != "" && m.ModelType != "" {
				models = append(models, fdModel{ref: childParent{id: id, arn: arn}, modelType: m.ModelType})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "frauddetector models")
	if err != nil {
		return nil, 0, 0, err
	}
	return models, total, inserted, nil
}

func scanFDExternalModels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetExternalModelsInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetExternalModels(ctx, input)
		if err != nil {
			if fdSoftError(st, "frauddetector:GetExternalModels", acct.ID, region, err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetExternalModels: %w", err)
		}
		for _, m := range out.ExternalModels {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorExternalModel, NativeID: arn,
				Name: m.ModelEndpoint, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector external-models")
}

// scanFDRules fans out GetRules per detector (GetRules requires a DetectorId).
// Each RuleDetail carries its own Arn; the resolver wires it to its detector.
func scanFDRules(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	detIDs, err := fdDetectorIDs(ctx, client)
	if err != nil {
		if fdSoftError(st, "frauddetector:GetDetectors", acct.ID, region, err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("frauddetector:GetDetectors: %w", err)
	}
	var batch []*store.Resource
	for _, did := range detIDs {
		input := &frauddetector.GetRulesInput{DetectorId: &did}
		for {
			out, rerr := client.GetRules(ctx, input)
			if rerr != nil {
				if fdSoftError(st, "frauddetector:GetRules", acct.ID, region, rerr) {
					break
				}
				return 0, 0, fmt.Errorf("frauddetector:GetRules %s: %w", did, rerr)
			}
			for _, r := range out.RuleDetails {
				arn := sv(r.Arn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeFraudDetectorRule, NativeID: arn,
					Name: r.RuleId, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "frauddetector rules")
}

func fdDetectorIDs(ctx context.Context, client fraudDetectorAPI) ([]string, error) {
	var ids []string
	input := &frauddetector.GetDetectorsInput{}
	for {
		out, err := client.GetDetectors(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, d := range out.Detectors {
			if id := sv(d.DetectorId); id != "" {
				ids = append(ids, id)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return ids, nil
}

// isFDResourceGone reports a parent or version deleted between its listing and
// the per-parent call. The closed-to-new-customers shape is not skipped here:
// gateFraudDetector already ends the scan for a closed account, so an
// empty-message AccessDenied at this point is a real denial.
func isFDResourceGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

func fdChildResource(acct *account, region, scanID, typ, arn, name, status string, attrs any) *store.Resource {
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
	if name != "" {
		r.Name = &name
	}
	if status != "" {
		r.Status = &status
	}
	return r
}

// scanFDDetectorVersions lists each detector's versions with DescribeDetector,
// whose summaries carry no ARN, then reads each with GetDetectorVersion for its
// ARN and its rules, model versions and external model endpoints.
func scanFDDetectorVersions(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent) (int, int, error) {
	const getOp = "frauddetector:GetDetectorVersion"
	var getDenied sync.Once
	return childFanOut(ctx, st, acct, region, "frauddetector:DescribeDetector", detectors, isFDResourceGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			input := &frauddetector.DescribeDetectorInput{DetectorId: &p.id}
			for {
				out, err := client.DescribeDetector(ctx, input)
				if err != nil {
					return nil, err
				}
				for _, s := range out.DetectorVersionSummaries {
					vid := sv(s.DetectorVersionId)
					if vid == "" {
						continue
					}
					v, err := client.GetDetectorVersion(ctx, &frauddetector.GetDetectorVersionInput{DetectorId: &p.id, DetectorVersionId: &vid})
					switch {
					case err == nil:
					case isFDResourceGone(err):
						continue
					case isAccessDenied(err):
						getDenied.Do(func() { _ = skipIfAccessDenied(st, getOp, acct.ID, region, err) })
						continue
					default:
						return nil, fmt.Errorf("GetDetectorVersion %s: %w", vid, err)
					}
					arn := sv(v.Arn)
					if arn == "" {
						continue
					}
					rows = append(rows, fdChildResource(acct, region, scanID, TypeFraudDetectorDetectorVersion, arn, vid, string(v.Status), v))
				}
				// DescribeDetector has no paginator; a repeated token would loop
				// forever.
				if done, err := lastPage(input.NextToken, out.NextToken); done {
					return rows, err
				}
				input.NextToken = out.NextToken
			}
		})
}

// fdModelVersionsPageSize is DescribeModelVersions' maximum MaxResults (1-10).
// The paginator sends no MaxResults at all when Limit is 0.
const fdModelVersionsPageSize = 10

func scanFDModelVersions(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, models []fdModel) (int, int, error) {
	parents := make([]childParent, len(models))
	modelTypes := make(map[string]frauddetectortypes.ModelTypeEnum, len(models))
	for i, m := range models {
		parents[i] = m.ref
		modelTypes[m.ref.arn] = m.modelType
	}
	return childFanOut(ctx, st, acct, region, "frauddetector:DescribeModelVersions", parents, isFDResourceGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := frauddetector.NewDescribeModelVersionsPaginator(client, &frauddetector.DescribeModelVersionsInput{
				ModelId: &p.id, ModelType: modelTypes[p.arn],
			}, func(o *frauddetector.DescribeModelVersionsPaginatorOptions) { o.Limit = fdModelVersionsPageSize })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range out.ModelVersionDetails {
					arn := sv(v.Arn)
					if arn == "" {
						continue
					}
					rows = append(rows, fdChildResource(acct, region, scanID, TypeFraudDetectorModelVersion, arn, sv(v.ModelVersionNumber), sv(v.Status), v))
				}
			}
			return rows, nil
		})
}
