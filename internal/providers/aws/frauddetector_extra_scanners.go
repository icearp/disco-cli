package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
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

func scanFDModels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetModelsInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetModels(ctx, input)
		if err != nil {
			if fdSoftError(st, "frauddetector:GetModels", acct.ID, region, err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetModels: %w", err)
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
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector models")
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
