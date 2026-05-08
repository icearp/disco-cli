package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ivs"
	"github.com/aws/aws-sdk-go-v2/service/ivsrealtime"
)

func init() {
	registerService(serviceEntry{
		name: "aws:ivs",
		fn:   scanIVS,
		emits: []coverage.TypeDecl{
			{Service: "ivs", DiscoType: TypeIVSChannel},
			{Service: "ivs", DiscoType: TypeIVSEncoderConfiguration, Leaf: true},
			{Service: "ivs", DiscoType: TypeIVSIngestConfiguration},
			{Service: "ivs", DiscoType: TypeIVSPlaybackKeyPair, Leaf: true},
			{Service: "ivs", DiscoType: TypeIVSPlaybackRestrictionPolicy, Leaf: true},
			{Service: "ivs", DiscoType: TypeIVSPublicKey, Leaf: true},
			{Service: "ivs", DiscoType: TypeIVSRecordingConfiguration},
			{Service: "ivs", DiscoType: TypeIVSStage},
			{Service: "ivs", DiscoType: TypeIVSStorageConfiguration},
			{Service: "ivs", DiscoType: TypeIVSStreamKey},
		},
	})
}

type ivsAPI interface {
	ListChannels(context.Context, *ivs.ListChannelsInput, ...func(*ivs.Options)) (*ivs.ListChannelsOutput, error)
	ListPlaybackKeyPairs(context.Context, *ivs.ListPlaybackKeyPairsInput, ...func(*ivs.Options)) (*ivs.ListPlaybackKeyPairsOutput, error)
	ListPlaybackRestrictionPolicies(context.Context, *ivs.ListPlaybackRestrictionPoliciesInput, ...func(*ivs.Options)) (*ivs.ListPlaybackRestrictionPoliciesOutput, error)
	ListRecordingConfigurations(context.Context, *ivs.ListRecordingConfigurationsInput, ...func(*ivs.Options)) (*ivs.ListRecordingConfigurationsOutput, error)
	ListStreamKeys(context.Context, *ivs.ListStreamKeysInput, ...func(*ivs.Options)) (*ivs.ListStreamKeysOutput, error)
}

type ivsRealtimeAPI interface {
	ListEncoderConfigurations(context.Context, *ivsrealtime.ListEncoderConfigurationsInput, ...func(*ivsrealtime.Options)) (*ivsrealtime.ListEncoderConfigurationsOutput, error)
	ListIngestConfigurations(context.Context, *ivsrealtime.ListIngestConfigurationsInput, ...func(*ivsrealtime.Options)) (*ivsrealtime.ListIngestConfigurationsOutput, error)
	ListPublicKeys(context.Context, *ivsrealtime.ListPublicKeysInput, ...func(*ivsrealtime.Options)) (*ivsrealtime.ListPublicKeysOutput, error)
	ListStages(context.Context, *ivsrealtime.ListStagesInput, ...func(*ivsrealtime.Options)) (*ivsrealtime.ListStagesOutput, error)
	ListStorageConfigurations(context.Context, *ivsrealtime.ListStorageConfigurationsInput, ...func(*ivsrealtime.Options)) (*ivsrealtime.ListStorageConfigurationsOutput, error)
}

func scanIVS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	c1 := ivs.NewFromConfig(acct.cfg, func(o *ivs.Options) { o.Region = region })
	c2 := ivsrealtime.NewFromConfig(acct.cfg, func(o *ivsrealtime.Options) { o.Region = region })

	// Phase 1: ivs channels (collect ARNs for per-channel stream-key fan-out).
	chARNs, t, i, ferr := scanIVSChannels(ctx, c1, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, ca := range chARNs {
		t, i, perr := scanIVSStreamKeys(ctx, c1, acct, region, st, scanID, ca)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIVSPlaybackKeyPairs(ctx, c1, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSPlaybackRestrictionPolicies(ctx, c1, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSRecordingConfigurations(ctx, c1, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSEncoderConfigurations(ctx, c2, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSIngestConfigurations(ctx, c2, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSPublicKeys(ctx, c2, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSStages(ctx, c2, acct, region, st, scanID) },
		func() (int, int, error) { return scanIVSStorageConfigurations(ctx, c2, acct, region, st, scanID) },
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

func scanIVSChannels(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := ivs.NewListChannelsPaginator(client, &ivs.ListChannelsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListChannels", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("ivs:ListChannels: %w", perr)
		}
		for _, c := range out.Channels {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSChannel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "ivs channels")
	return arns, t, i, err
}

func scanIVSStreamKeys(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID, channelARN string) (int, int, error) {
	ca := channelARN
	pager := ivs.NewListStreamKeysPaginator(client, &ivs.ListStreamKeysInput{ChannelArn: &ca})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListStreamKeys", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListStreamKeys: %w", perr)
		}
		for _, k := range out.StreamKeys {
			arn := sv(k.Arn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSStreamKey, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs stream-keys")
}

func scanIVSPlaybackKeyPairs(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivs.NewListPlaybackKeyPairsPaginator(client, &ivs.ListPlaybackKeyPairsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListPlaybackKeyPairs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListPlaybackKeyPairs: %w", perr)
		}
		for _, k := range out.KeyPairs {
			arn := sv(k.Arn)
			if arn == "" {
				continue
			}
			label := sv(k.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSPlaybackKeyPair, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs playback-key-pairs")
}

func scanIVSPlaybackRestrictionPolicies(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivs.NewListPlaybackRestrictionPoliciesPaginator(client, &ivs.ListPlaybackRestrictionPoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListPlaybackRestrictionPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListPlaybackRestrictionPolicies: %w", perr)
		}
		for _, p := range out.PlaybackRestrictionPolicies {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSPlaybackRestrictionPolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs playback-restriction-policies")
}

func scanIVSRecordingConfigurations(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivs.NewListRecordingConfigurationsPaginator(client, &ivs.ListRecordingConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListRecordingConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListRecordingConfigurations: %w", perr)
		}
		for _, r := range out.RecordingConfigurations {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSRecordingConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs recording-configurations")
}

func scanIVSEncoderConfigurations(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivsrealtime.NewListEncoderConfigurationsPaginator(client, &ivsrealtime.ListEncoderConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListEncoderConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListEncoderConfigurations: %w", perr)
		}
		for _, e := range out.EncoderConfigurations {
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
				Type: TypeIVSEncoderConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs encoder-configurations")
}

func scanIVSIngestConfigurations(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivsrealtime.NewListIngestConfigurationsPaginator(client, &ivsrealtime.ListIngestConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListIngestConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListIngestConfigurations: %w", perr)
		}
		for _, ic := range out.IngestConfigurations {
			arn := sv(ic.Arn)
			if arn == "" {
				continue
			}
			label := sv(ic.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSIngestConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ic), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs ingest-configurations")
}

func scanIVSPublicKeys(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivsrealtime.NewListPublicKeysPaginator(client, &ivsrealtime.ListPublicKeysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListPublicKeys", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListPublicKeys: %w", perr)
		}
		for _, k := range out.PublicKeys {
			arn := sv(k.Arn)
			if arn == "" {
				continue
			}
			label := sv(k.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSPublicKey, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs public-keys")
}

func scanIVSStages(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivsrealtime.NewListStagesPaginator(client, &ivsrealtime.ListStagesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListStages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListStages: %w", perr)
		}
		for _, s := range out.Stages {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSStage, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs stages")
}

func scanIVSStorageConfigurations(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ivsrealtime.NewListStorageConfigurationsPaginator(client, &ivsrealtime.ListStorageConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ivs:ListStorageConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ivs:ListStorageConfigurations: %w", perr)
		}
		for _, sc := range out.StorageConfigurations {
			arn := sv(sc.Arn)
			if arn == "" {
				continue
			}
			label := sv(sc.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSStorageConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ivs storage-configurations")
}
