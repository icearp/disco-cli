package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
	frauddetectortypes "github.com/aws/aws-sdk-go-v2/service/frauddetector/types"
	"github.com/icearp/disco-cli/store"
)

// scanFDModels stores the models with their tags. It returns the stored ones
// that carry a ModelId and a ModelType, the parents of the model version
// phase (DescribeModelVersions keys on both), and each stored model's type
// by ARN.
func scanFDModels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) ([]childParent, map[string]frauddetectortypes.ModelTypeEnum, bool) {
	modelTypes := map[string]frauddetectortypes.ModelTypeEnum{}
	models, stop := storeParentListTags(ctx, st, acct, region, "frauddetector:GetModels", "models", "ModelId or ModelType",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.Model, *string, error) {
			out, err := client.GetModels(ctx, &frauddetector.GetModelsInput{NextToken: token, MaxResults: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.Models, out.NextToken, nil
		}),
		func(m frauddetectortypes.Model) (*store.Resource, string, string) {
			arn := sv(m.Arn)
			if arn == "" {
				return fdNoARN("models", sv(m.ModelId))
			}
			modelTypes[arn] = m.ModelType
			return fdRow(acct, region, scanID, TypeFraudDetectorModel, arn, m.ModelId, m), "", ""
		},
		func(m frauddetectortypes.Model) string {
			if m.ModelType == "" {
				return ""
			}
			return sv(m.ModelId)
		},
		tags, p)
	return models, modelTypes, stop
}

func scanFDExternalModels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetExternalModels",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.ExternalModel, *string, error) {
			out, err := client.GetExternalModels(ctx, &frauddetector.GetExternalModelsInput{NextToken: token, MaxResults: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.ExternalModels, out.NextToken, nil
		}),
		func(m frauddetectortypes.ExternalModel) (*store.Resource, string, string) {
			arn := sv(m.Arn)
			if arn == "" {
				return fdNoARN("external models", sv(m.ModelEndpoint))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorExternalModel, arn, m.ModelEndpoint, m), "", ""
		},
		tags, p)
}

// scanFDRules stores the rules of each stored detector with their tags
// (GetRules requires a DetectorId). The resolver links each rule to its
// detector.
func scanFDRules(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, stop := storeEachParentTags(ctx, st, acct, region, "frauddetector:GetRules", fdNotFound, detectors,
		func(ctx context.Context, det string, token *string) ([]frauddetectortypes.RuleDetail, *string, error) {
			out, err := client.GetRules(ctx, &frauddetector.GetRulesInput{DetectorId: &det, NextToken: token, MaxResults: sdkaws.Int32(100)})
			if err != nil {
				return nil, nil, err
			}
			return out.RuleDetails, out.NextToken, nil
		},
		func(det string, r frauddetectortypes.RuleDetail) (*store.Resource, string, string) {
			arn := sv(r.Arn)
			if arn == "" {
				return nil, "rules have an empty Arn", cmp.Or(sv(r.RuleId), "(a rule with no id)") + " of detector " + det
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorRule, arn, r.RuleId, r), "", ""
		}, tags, p)
	return stop
}

// fdChildRow is fdRow with the Name and Status set only when not empty.
func fdChildRow(acct *account, region, scanID, typ, arn, name, status string, item any) *store.Resource {
	r := fdRow(acct, region, scanID, typ, arn, nonEmptyPtr(name), item)
	r.Status = nonEmptyPtr(status)
	return r
}

const fdGetDetectorVersionOp = "frauddetector:GetDetectorVersion"

// fdVersionFailure is a GetDetectorVersion call that failed for another
// reason than the version's deletion or ctx's end.
type fdVersionFailure struct {
	key string
	err error
}

// scanFDDetectorVersions stores the versions of each stored detector with
// their tags, under the detector. DescribeDetector lists a detector's
// versions as summaries without an ARN, so each is read with
// GetDetectorVersion, whose body is the row and carries the ARN. A version
// that read finds deleted is skipped. Any other failed read drops the
// version, which has no ARN to be stored under: a denial is warned about
// once, under GetDetectorVersion, after the write; any other error is
// returned, apart from the transient ones so that those can be demoted
// beside a hard error. A version without an ARN is warned about.
//
// The hierarchy is recorded after storeEachParentTags, which has no parentOf
// hook; the warnings it sent first concern rows it already wrote.
func scanFDDetectorVersions(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, tags rowTagReader, p *phaseTotals) bool {
	const op = "frauddetector:DescribeDetector"
	detectorARN := make(map[string]string, len(detectors))
	for _, d := range detectors {
		detectorARN[d.id] = d.arn
	}
	var (
		mu sync.Mutex
		// failures holds each detector's failed reads, so that they are
		// classified in detector order after the concurrent listings.
		failures = map[string][]fdVersionFailure{}
		parentOf = map[*store.Resource]string{}
	)
	stored, stop := storeEachParentTags(ctx, st, acct, region, op, fdNotFound, detectors,
		func(ctx context.Context, det string, token *string) ([]*frauddetector.GetDetectorVersionOutput, *string, error) {
			out, err := client.DescribeDetector(ctx, &frauddetector.DescribeDetectorInput{DetectorId: &det, NextToken: token, MaxResults: sdkaws.Int32(2500)})
			if err != nil {
				return nil, nil, err
			}
			versions := make([]*frauddetector.GetDetectorVersionOutput, 0, len(out.DetectorVersionSummaries))
			for _, s := range out.DetectorVersionSummaries {
				vid := sv(s.DetectorVersionId)
				if vid == "" {
					// Carries no id, so the row builder warns about it.
					versions = append(versions, &frauddetector.GetDetectorVersionOutput{})
					continue
				}
				v, err := client.GetDetectorVersion(ctx, &frauddetector.GetDetectorVersionInput{DetectorId: &det, DetectorVersionId: &vid})
				switch {
				case err == nil:
					versions = append(versions, v)
				case isAPIErrorCode(err, fdNotFound...):
				case ctx.Err() != nil && errors.Is(err, ctx.Err()):
					return nil, nil, err
				default:
					mu.Lock()
					failures[det] = append(failures[det], fdVersionFailure{det + "/" + vid, err})
					mu.Unlock()
				}
			}
			return versions, out.NextToken, nil
		},
		func(det string, v *frauddetector.GetDetectorVersionOutput) (*store.Resource, string, string) {
			vid := sv(v.DetectorVersionId)
			if vid == "" {
				return nil, "detector versions have an empty DetectorVersionId", "(a version with no id) of detector " + det
			}
			arn := sv(v.Arn)
			if arn == "" {
				return nil, "detector versions have an empty Arn", det + "/" + vid
			}
			r := fdChildRow(acct, region, scanID, TypeFraudDetectorDetectorVersion, arn, vid, string(v.Status), v)
			parentOf[r] = detectorARN[det]
			return r, "", ""
		}, tags, p)
	var (
		denied     skipTally
		transients transientTally
		errs       []error
	)
	for _, d := range detectors {
		for _, f := range failures[d.id] {
			switch {
			case isAccessDenied(f.err):
				denied.skipped(f.key, f.err)
			// An NXDOMAIN tallied here is flattened by runHostPhases
			// (resultLateNXDOMAIN): the detectors listed were stored.
			case transients.drop(f.key, f.err):
			default:
				errs = append(errs, fmt.Errorf("%s %s: %w", fdGetDetectorVersionOp, f.key, f.err))
			}
		}
	}
	hard := errors.Join(errs...)
	var transient error
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: %d detector version(s) not read, first %s: %w", fdGetDetectorVersionOp, t.count, t.first, t.err)
	}
	if !stop {
		stop = recordUnderParents(st, acct, op, stored, parentOf, p)
	}
	if stop {
		// Joined into the failed write's error, as rowTagErrs.addPhase does:
		// demoted on its own, the transient one would be a warning sent
		// after the write failed.
		for i := range p.errs {
			if errors.Is(p.errs[i].err, store.ErrStoreWrite) {
				p.errs[i].err = errors.Join(p.errs[i].err, hard, transient)
				break
			}
		}
		return true
	}
	p.errs.add(fdGetDetectorVersionOp, hard)
	p.errs.add(fdGetDetectorVersionOp, transient)
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, fdGetDetectorVersionOp, acct.ID, region, fmt.Errorf("%d detector version(s) not stored, first %s: %w", denied.count, denied.first, denied.err))
	}
	return false
}

// scanFDModelVersions stores the versions of each stored model with their
// tags, under the model. A model is keyed by its ARN here, as its ModelId is
// unique only within its ModelType; modelTypes holds each one's type.
//
// The hierarchy is recorded after storeEachParentTags, which has no parentOf
// hook; the warnings it sent first concern rows it already wrote.
func scanFDModelVersions(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, models []childParent, modelTypes map[string]frauddetectortypes.ModelTypeEnum, tags rowTagReader, p *phaseTotals) bool {
	const op = "frauddetector:DescribeModelVersions"
	byARN := make([]childParent, len(models))
	modelID := make(map[string]string, len(models))
	for i, m := range models {
		byARN[i] = childParent{id: m.arn, arn: m.arn}
		modelID[m.arn] = m.id
	}
	parentOf := map[*store.Resource]string{}
	stored, stop := storeEachParentTags(ctx, st, acct, region, op, fdNotFound, byARN,
		func(ctx context.Context, model string, token *string) ([]frauddetectortypes.ModelVersionDetail, *string, error) {
			out, err := client.DescribeModelVersions(ctx, &frauddetector.DescribeModelVersionsInput{
				ModelId: sdkaws.String(modelID[model]), ModelType: modelTypes[model], NextToken: token, MaxResults: sdkaws.Int32(10),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelVersionDetails, out.NextToken, nil
		},
		func(model string, v frauddetectortypes.ModelVersionDetail) (*store.Resource, string, string) {
			arn := sv(v.Arn)
			if arn == "" {
				return nil, "model versions have an empty Arn", cmp.Or(sv(v.ModelVersionNumber), "(a version with no number)") + " of model " + model
			}
			r := fdChildRow(acct, region, scanID, TypeFraudDetectorModelVersion, arn, sv(v.ModelVersionNumber), sv(v.Status), v)
			parentOf[r] = model
			return r, "", ""
		}, tags, p)
	if stop {
		return true
	}
	return recordUnderParents(st, acct, op, stored, parentOf, p)
}
