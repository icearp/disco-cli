package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ivs"
	ivstypes "github.com/aws/aws-sdk-go-v2/service/ivs/types"
	"github.com/aws/aws-sdk-go-v2/service/ivsrealtime"
	ivsrealtimetypes "github.com/aws/aws-sdk-go-v2/service/ivsrealtime/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIVSAdConfiguration, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSChannel, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSEncoderConfiguration, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSIngestConfiguration, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSPlaybackKeyPair, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSPlaybackRestrictionPolicy, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSPublicKey, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSRecordingConfiguration, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSStage, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSStorageConfiguration, Service: "ivs"})
	registerType(restype.Descriptor{Type: TypeIVSStreamKey, Service: "ivs"})
	registerService(serviceEntry{
		name: "aws:ivs",
		fn:   scanIVS,
	})
}

type ivsAPI interface {
	ListAdConfigurations(context.Context, *ivs.ListAdConfigurationsInput, ...func(*ivs.Options)) (*ivs.ListAdConfigurationsOutput, error)
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

// The IVS real-time ops share the ivs IAM namespace.
const (
	ivsListChannelsOp                    = "ivs:ListChannels"
	ivsListStreamKeysOp                  = "ivs:ListStreamKeys"
	ivsListAdConfigurationsOp            = "ivs:ListAdConfigurations"
	ivsListPlaybackKeyPairsOp            = "ivs:ListPlaybackKeyPairs"
	ivsListPlaybackRestrictionPoliciesOp = "ivs:ListPlaybackRestrictionPolicies"
	ivsListRecordingConfigurationsOp     = "ivs:ListRecordingConfigurations"
	ivsListEncoderConfigurationsOp       = "ivs:ListEncoderConfigurations"
	ivsListIngestConfigurationsOp        = "ivs:ListIngestConfigurations"
	ivsListPublicKeysOp                  = "ivs:ListPublicKeys"
	ivsListStagesOp                      = "ivs:ListStages"
	ivsListStorageConfigurationsOp       = "ivs:ListStorageConfigurations"
)

// Page caps from the Smithy models. Unset, ListStreamKeys and
// ListPlaybackRestrictionPolicies default to one item per page.
var (
	ivsMaxResults          = sdkaws.Int32(100)
	ivsStreamKeyMaxResults = sdkaws.Int32(50)
)

func scanIVS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	c1 := ivs.NewFromConfig(acct.cfg, func(o *ivs.Options) { o.Region = region })
	c2 := ivsrealtime.NewFromConfig(acct.cfg, func(o *ivsrealtime.Options) { o.Region = region })
	return scanIVSAll(ctx, c1, c2, acct, region, st, scanID)
}

// scanIVSAll runs every phase and joins their errors; only a store write
// failure stops it. A failed channel listing leaves the stream-key phase
// fewer channels to list.
func scanIVSAll(ctx context.Context, c1 ivsAPI, c2 ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	chARNs, t, i, ferr := scanIVSChannels(ctx, c1, acct, region, st, scanID)
	if phases.addPhase(ivsListChannelsOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{ivsListStreamKeysOp, func() (int, int, error) { return scanIVSStreamKeys(ctx, c1, acct, region, st, scanID, chARNs) }},
		{ivsListAdConfigurationsOp, func() (int, int, error) { return scanIVSAdConfigurations(ctx, c1, acct, region, st, scanID) }},
		{ivsListPlaybackKeyPairsOp, func() (int, int, error) { return scanIVSPlaybackKeyPairs(ctx, c1, acct, region, st, scanID) }},
		{ivsListPlaybackRestrictionPoliciesOp, func() (int, int, error) {
			return scanIVSPlaybackRestrictionPolicies(ctx, c1, acct, region, st, scanID)
		}},
		{ivsListRecordingConfigurationsOp, func() (int, int, error) {
			return scanIVSRecordingConfigurations(ctx, c1, acct, region, st, scanID)
		}},
		{ivsListEncoderConfigurationsOp, func() (int, int, error) {
			return scanIVSEncoderConfigurations(ctx, c2, acct, region, st, scanID)
		}},
		{ivsListIngestConfigurationsOp, func() (int, int, error) {
			return scanIVSIngestConfigurations(ctx, c2, acct, region, st, scanID)
		}},
		{ivsListPublicKeysOp, func() (int, int, error) { return scanIVSPublicKeys(ctx, c2, acct, region, st, scanID) }},
		{ivsListStagesOp, func() (int, int, error) { return scanIVSStages(ctx, c2, acct, region, st, scanID) }},
		{ivsListStorageConfigurationsOp, func() (int, int, error) {
			return scanIVSStorageConfigurations(ctx, c2, acct, region, st, scanID)
		}},
	} {
		t, i, ferr := phase.fn()
		if phases.addPhase(phase.op, t, i, ferr) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// ivsRow builds the row of a listed IVS element, named by name or, when that
// is empty, by its ARN. The caller has checked arn is non-empty.
func ivsRow(acct *account, region, scanID, rtype, arn, name string, element any, tags *string) *store.Resource {
	if name == "" {
		name = arn
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: arn, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: tags, DiscoveredBy: scanID,
	}
}

func scanIVSAdConfigurations(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListAdConfigurationsOp,
		func(ctx context.Context, token *string) ([]ivstypes.AdConfigurationSummary, *string, error) {
			out, err := client.ListAdConfigurations(ctx, &ivs.ListAdConfigurationsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AdConfigurations, out.NextToken, nil
		},
		func(a ivstypes.AdConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(a.Arn)
			if arn == "" {
				return nil, "ad configurations have an empty Arn", sv(a.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSAdConfiguration, arn, sv(a.Name), a, mapTagsJSON(a.Tags)), "", ""
		})
}

// scanIVSChannels stores the channels and returns the ARNs of those stored,
// for the stream-key phase.
func scanIVSChannels(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var arns []string
	t, i, err := storeTokenList(ctx, st, acct, region, ivsListChannelsOp,
		func(ctx context.Context, token *string) ([]ivstypes.ChannelSummary, *string, error) {
			out, err := client.ListChannels(ctx, &ivs.ListChannelsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Channels, out.NextToken, nil
		},
		func(c ivstypes.ChannelSummary) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return nil, "channels have an empty Arn, and their stream keys were not read either", sv(c.Name)
			}
			arns = append(arns, arn)
			return ivsRow(acct, region, scanID, TypeIVSChannel, arn, sv(c.Name), c, mapTagsJSON(c.Tags)), "", ""
		})
	return arns, t, i, err
}

// scanIVSStreamKeys stores the stream keys of each channel. A stream key has
// no name, so its row is named by its ARN.
func scanIVSStreamKeys(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string, channelARNs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, ivsListStreamKeysOp, []string{"ResourceNotFoundException"}, channelARNs,
		func(ctx context.Context, channelARN string, token *string) ([]ivstypes.StreamKeySummary, *string, error) {
			out, err := client.ListStreamKeys(ctx, &ivs.ListStreamKeysInput{ChannelArn: &channelARN, NextToken: token, MaxResults: ivsStreamKeyMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.StreamKeys, out.NextToken, nil
		},
		func(channelARN string, k ivstypes.StreamKeySummary) (*store.Resource, string, string) {
			arn := sv(k.Arn)
			if arn == "" {
				return nil, "stream keys have an empty Arn (first names its channel)", channelARN
			}
			return ivsRow(acct, region, scanID, TypeIVSStreamKey, arn, "", k, mapTagsJSON(k.Tags)), "", ""
		})
}

func scanIVSPlaybackKeyPairs(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListPlaybackKeyPairsOp,
		func(ctx context.Context, token *string) ([]ivstypes.PlaybackKeyPairSummary, *string, error) {
			out, err := client.ListPlaybackKeyPairs(ctx, &ivs.ListPlaybackKeyPairsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.KeyPairs, out.NextToken, nil
		},
		func(k ivstypes.PlaybackKeyPairSummary) (*store.Resource, string, string) {
			arn := sv(k.Arn)
			if arn == "" {
				return nil, "playback key pairs have an empty Arn", sv(k.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSPlaybackKeyPair, arn, sv(k.Name), k, mapTagsJSON(k.Tags)), "", ""
		})
}

func scanIVSPlaybackRestrictionPolicies(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListPlaybackRestrictionPoliciesOp,
		func(ctx context.Context, token *string) ([]ivstypes.PlaybackRestrictionPolicySummary, *string, error) {
			out, err := client.ListPlaybackRestrictionPolicies(ctx, &ivs.ListPlaybackRestrictionPoliciesInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.PlaybackRestrictionPolicies, out.NextToken, nil
		},
		func(p ivstypes.PlaybackRestrictionPolicySummary) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "playback restriction policies have an empty Arn", sv(p.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSPlaybackRestrictionPolicy, arn, sv(p.Name), p, mapTagsJSON(p.Tags)), "", ""
		})
}

func scanIVSRecordingConfigurations(ctx context.Context, client ivsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListRecordingConfigurationsOp,
		func(ctx context.Context, token *string) ([]ivstypes.RecordingConfigurationSummary, *string, error) {
			out, err := client.ListRecordingConfigurations(ctx, &ivs.ListRecordingConfigurationsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.RecordingConfigurations, out.NextToken, nil
		},
		func(r ivstypes.RecordingConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(r.Arn)
			if arn == "" {
				return nil, "recording configurations have an empty Arn", sv(r.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSRecordingConfiguration, arn, sv(r.Name), r, mapTagsJSON(r.Tags)), "", ""
		})
}

func scanIVSEncoderConfigurations(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListEncoderConfigurationsOp,
		func(ctx context.Context, token *string) ([]ivsrealtimetypes.EncoderConfigurationSummary, *string, error) {
			out, err := client.ListEncoderConfigurations(ctx, &ivsrealtime.ListEncoderConfigurationsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.EncoderConfigurations, out.NextToken, nil
		},
		func(e ivsrealtimetypes.EncoderConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(e.Arn)
			if arn == "" {
				return nil, "encoder configurations have an empty Arn", sv(e.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSEncoderConfiguration, arn, sv(e.Name), e, mapTagsJSON(e.Tags)), "", ""
		})
}

// scanIVSIngestConfigurations stores no tags: IngestConfigurationSummary
// carries none.
func scanIVSIngestConfigurations(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListIngestConfigurationsOp,
		func(ctx context.Context, token *string) ([]ivsrealtimetypes.IngestConfigurationSummary, *string, error) {
			out, err := client.ListIngestConfigurations(ctx, &ivsrealtime.ListIngestConfigurationsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.IngestConfigurations, out.NextToken, nil
		},
		func(ic ivsrealtimetypes.IngestConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(ic.Arn)
			if arn == "" {
				return nil, "ingest configurations have an empty Arn", sv(ic.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSIngestConfiguration, arn, sv(ic.Name), ic, nil), "", ""
		})
}

func scanIVSPublicKeys(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListPublicKeysOp,
		func(ctx context.Context, token *string) ([]ivsrealtimetypes.PublicKeySummary, *string, error) {
			out, err := client.ListPublicKeys(ctx, &ivsrealtime.ListPublicKeysInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.PublicKeys, out.NextToken, nil
		},
		func(k ivsrealtimetypes.PublicKeySummary) (*store.Resource, string, string) {
			arn := sv(k.Arn)
			if arn == "" {
				return nil, "public keys have an empty Arn", sv(k.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSPublicKey, arn, sv(k.Name), k, mapTagsJSON(k.Tags)), "", ""
		})
}

func scanIVSStages(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListStagesOp,
		func(ctx context.Context, token *string) ([]ivsrealtimetypes.StageSummary, *string, error) {
			out, err := client.ListStages(ctx, &ivsrealtime.ListStagesInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Stages, out.NextToken, nil
		},
		func(s ivsrealtimetypes.StageSummary) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return nil, "stages have an empty Arn", sv(s.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSStage, arn, sv(s.Name), s, mapTagsJSON(s.Tags)), "", ""
		})
}

func scanIVSStorageConfigurations(ctx context.Context, client ivsRealtimeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ivsListStorageConfigurationsOp,
		func(ctx context.Context, token *string) ([]ivsrealtimetypes.StorageConfigurationSummary, *string, error) {
			out, err := client.ListStorageConfigurations(ctx, &ivsrealtime.ListStorageConfigurationsInput{NextToken: token, MaxResults: ivsMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.StorageConfigurations, out.NextToken, nil
		},
		func(sc ivsrealtimetypes.StorageConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(sc.Arn)
			if arn == "" {
				return nil, "storage configurations have an empty Arn", sv(sc.Name)
			}
			return ivsRow(acct, region, scanID, TypeIVSStorageConfiguration, arn, sv(sc.Name), sc, mapTagsJSON(sc.Tags)), "", ""
		})
}
