package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
)

func init() {
	registerService(serviceEntry{
		name: "aws:frauddetector",
		fn:   scanFraudDetector,
		emits: []coverage.TypeDecl{
			{Service: "frauddetector", DiscoType: TypeFraudDetectorDetector},
			{Service: "frauddetector", DiscoType: TypeFraudDetectorEntityType},
			{Service: "frauddetector", DiscoType: TypeFraudDetectorEventType},
			{Service: "frauddetector", DiscoType: TypeFraudDetectorLabel},
			{Service: "frauddetector", DiscoType: TypeFraudDetectorList},
			{Service: "frauddetector", DiscoType: TypeFraudDetectorOutcome},
			{Service: "frauddetector", DiscoType: TypeFraudDetectorVariable},
		},
	})
}

type fraudDetectorAPI interface {
	GetDetectors(context.Context, *frauddetector.GetDetectorsInput, ...func(*frauddetector.Options)) (*frauddetector.GetDetectorsOutput, error)
	GetEntityTypes(context.Context, *frauddetector.GetEntityTypesInput, ...func(*frauddetector.Options)) (*frauddetector.GetEntityTypesOutput, error)
	GetEventTypes(context.Context, *frauddetector.GetEventTypesInput, ...func(*frauddetector.Options)) (*frauddetector.GetEventTypesOutput, error)
	GetLabels(context.Context, *frauddetector.GetLabelsInput, ...func(*frauddetector.Options)) (*frauddetector.GetLabelsOutput, error)
	GetListsMetadata(context.Context, *frauddetector.GetListsMetadataInput, ...func(*frauddetector.Options)) (*frauddetector.GetListsMetadataOutput, error)
	GetOutcomes(context.Context, *frauddetector.GetOutcomesInput, ...func(*frauddetector.Options)) (*frauddetector.GetOutcomesOutput, error)
	GetVariables(context.Context, *frauddetector.GetVariablesInput, ...func(*frauddetector.Options)) (*frauddetector.GetVariablesOutput, error)
}

func scanFraudDetector(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := frauddetector.NewFromConfig(acct.cfg, func(o *frauddetector.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanFDDetectors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFDEntityTypes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFDEventTypes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFDLabels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFDLists(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFDOutcomes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFDVariables(ctx, client, acct, region, st, scanID) },
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

func scanFDDetectors(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetDetectorsInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetDetectors(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetDetectors", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetDetectors: %w", err)
		}
		for _, d := range out.Detectors {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.DetectorId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorDetector, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector detectors")
}

func scanFDEntityTypes(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetEntityTypesInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetEntityTypes(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetEntityTypes", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetEntityTypes: %w", err)
		}
		for _, e := range out.EntityTypes {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			label := sv(e.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorEntityType, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector entity-types")
}

func scanFDEventTypes(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetEventTypesInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetEventTypes(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetEventTypes", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetEventTypes: %w", err)
		}
		for _, e := range out.EventTypes {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			label := sv(e.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorEventType, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector event-types")
}

func scanFDLabels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetLabelsInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetLabels(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetLabels", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetLabels: %w", err)
		}
		for _, l := range out.Labels {
			arn := sv(l.Arn)
			if arn == "" {
				continue
			}
			label := sv(l.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorLabel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector labels")
}

func scanFDLists(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetListsMetadataInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetListsMetadata(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetListsMetadata", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetListsMetadata: %w", err)
		}
		for _, l := range out.Lists {
			arn := sv(l.Arn)
			if arn == "" {
				continue
			}
			label := sv(l.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorList, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector lists")
}

func scanFDOutcomes(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetOutcomesInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetOutcomes(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetOutcomes", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetOutcomes: %w", err)
		}
		for _, o := range out.Outcomes {
			arn := sv(o.Arn)
			if arn == "" {
				continue
			}
			label := sv(o.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorOutcome, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector outcomes")
}

func scanFDVariables(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &frauddetector.GetVariablesInput{}
	var batch []*store.Resource
	for {
		out, err := client.GetVariables(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "frauddetector:GetVariables", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("frauddetector:GetVariables: %w", err)
		}
		for _, v := range out.Variables {
			arn := sv(v.Arn)
			if arn == "" {
				continue
			}
			label := sv(v.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFraudDetectorVariable, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "frauddetector variables")
}
