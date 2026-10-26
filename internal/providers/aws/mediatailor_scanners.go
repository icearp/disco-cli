package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediatailor"
	mttypes "github.com/aws/aws-sdk-go-v2/service/mediatailor/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaTailorChannel, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorChannelPolicy, Service: "mediatailor"})
	// An HTTP_REQUEST function calls an external service, so its URL (query
	// string or userinfo), header values and request body can carry that
	// service's API key or bearer token. All three are redacted whole because
	// the redact engine has no value-classifying mode yet; move them to it when
	// it lands, so non-secret values are kept.
	registerType(restype.Descriptor{Type: TypeMediaTailorFunction, Service: "mediatailor", Redact: []redact.Rule{
		{Path: "HttpRequestConfiguration.Url", Mode: redact.RedactScalar},
		{Path: "HttpRequestConfiguration.Headers.*", Mode: redact.RedactScalar},
		{Path: "HttpRequestConfiguration.Body", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeMediaTailorLiveSource, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorPlaybackConfiguration, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorPrefetchSchedule, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorProgram, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorSourceLocation, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorVodSource, Service: "mediatailor"})
	registerService(serviceEntry{
		name: "aws:mediatailor",
		fn:   scanMediaTailor,
	})
}

type mediatailorAPI interface {
	ListChannels(context.Context, *mediatailor.ListChannelsInput, ...func(*mediatailor.Options)) (*mediatailor.ListChannelsOutput, error)
	GetChannelPolicy(context.Context, *mediatailor.GetChannelPolicyInput, ...func(*mediatailor.Options)) (*mediatailor.GetChannelPolicyOutput, error)
	GetChannelSchedule(context.Context, *mediatailor.GetChannelScheduleInput, ...func(*mediatailor.Options)) (*mediatailor.GetChannelScheduleOutput, error)
	ListPrefetchSchedules(context.Context, *mediatailor.ListPrefetchSchedulesInput, ...func(*mediatailor.Options)) (*mediatailor.ListPrefetchSchedulesOutput, error)
	ListSourceLocations(context.Context, *mediatailor.ListSourceLocationsInput, ...func(*mediatailor.Options)) (*mediatailor.ListSourceLocationsOutput, error)
	ListLiveSources(context.Context, *mediatailor.ListLiveSourcesInput, ...func(*mediatailor.Options)) (*mediatailor.ListLiveSourcesOutput, error)
	ListVodSources(context.Context, *mediatailor.ListVodSourcesInput, ...func(*mediatailor.Options)) (*mediatailor.ListVodSourcesOutput, error)
	ListPlaybackConfigurations(context.Context, *mediatailor.ListPlaybackConfigurationsInput, ...func(*mediatailor.Options)) (*mediatailor.ListPlaybackConfigurationsOutput, error)
	ListFunctions(context.Context, *mediatailor.ListFunctionsInput, ...func(*mediatailor.Options)) (*mediatailor.ListFunctionsOutput, error)
}

const (
	mtListChannelsOp               = "mediatailor:ListChannels"
	mtGetChannelPolicyOp           = "mediatailor:GetChannelPolicy"
	mtGetChannelScheduleOp         = "mediatailor:GetChannelSchedule"
	mtListSourceLocationsOp        = "mediatailor:ListSourceLocations"
	mtListLiveSourcesOp            = "mediatailor:ListLiveSources"
	mtListVodSourcesOp             = "mediatailor:ListVodSources"
	mtListPlaybackConfigurationsOp = "mediatailor:ListPlaybackConfigurations"
	mtListPrefetchSchedulesOp      = "mediatailor:ListPrefetchSchedules"
	mtListFunctionsOp              = "mediatailor:ListFunctions"
)

// The Smithy model caps every MediaTailor list page at 100.
var mtMaxResults = sdkaws.Int32(100)

func scanMediaTailor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := mediatailor.NewFromConfig(acct.cfg, func(o *mediatailor.Options) { o.Region = region })
	return scanMediaTailorAll(ctx, client, acct, region, st, scanID)
}

// scanMediaTailorAll runs every phase and joins their errors; only a store
// write failure stops it. A failed parent listing leaves its child phases
// fewer parents to read.
func scanMediaTailorAll(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		phases                      phaseTotals
		channels, locations, config []string
	)
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{mtListChannelsOp, func() (t, i int, err error) {
			channels, t, i, err = scanMTChannels(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{mtGetChannelPolicyOp, func() (int, int, error) {
			return scanMTChannelPolicies(ctx, client, acct, region, st, scanID, channels)
		}},
		{mtGetChannelScheduleOp, func() (int, int, error) { return scanMTPrograms(ctx, client, acct, region, st, scanID, channels) }},
		{mtListSourceLocationsOp, func() (t, i int, err error) {
			locations, t, i, err = scanMTSourceLocations(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{mtListLiveSourcesOp, func() (int, int, error) { return scanMTLiveSources(ctx, client, acct, region, st, scanID, locations) }},
		{mtListVodSourcesOp, func() (int, int, error) { return scanMTVodSources(ctx, client, acct, region, st, scanID, locations) }},
		{mtListPlaybackConfigurationsOp, func() (t, i int, err error) {
			config, t, i, err = scanMTPlaybackConfigurations(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{mtListPrefetchSchedulesOp, func() (int, int, error) {
			return scanMTPrefetchSchedules(ctx, client, acct, region, st, scanID, config)
		}},
		{mtListFunctionsOp, func() (int, int, error) { return scanMTFunctions(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, err := phase.fn()
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// mtRow builds the row of a listed MediaTailor element. The caller has checked
// arn is non-empty.
func mtRow(acct *account, region, scanID, rtype, arn string, name *string, element any, tags *string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: arn, Name: name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: tags, DiscoveredBy: scanID,
	}
}

// storeMTParents stores an account-wide parent listing and returns the names
// of the rows stored, which the child reads key on. A row stored without a
// name gets one warning, after the write succeeded, since its children were
// not read.
func storeMTParents[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what, nameField string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
) ([]string, int, int, error) {
	var (
		names  []string
		noName skipTally
	)
	t, i, err := storeTokenList(ctx, st, acct, region, op, page, func(it Item) (*store.Resource, string, string) {
		r, why, key := row(it)
		switch {
		case r == nil:
		case sv(r.Name) == "":
			noName.skipped(r.NativeID, nil)
		default:
			names = append(names, sv(r.Name))
		}
		return r, why, key
	})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, op, acct, region, &noName, what, nameField)
	}
	return names, t, i, err
}

func scanMTChannels(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	return storeMTParents(ctx, st, acct, region, mtListChannelsOp, "channel(s)", "ChannelName",
		func(ctx context.Context, token *string) ([]mttypes.Channel, *string, error) {
			out, err := client.ListChannels(ctx, &mediatailor.ListChannelsInput{NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(c mttypes.Channel) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return nil, "channels have an empty Arn, and their policies and schedules were not read either", sv(c.ChannelName)
			}
			r := mtRow(acct, region, scanID, TypeMediaTailorChannel, arn, c.ChannelName, c, mapTagsJSON(c.Tags))
			r.Status = c.ChannelState
			return r, "", ""
		})
}

// scanMTChannelPolicies stores each channel's IAM policy. The read returns
// only the policy text, so the row's NativeID is the channel's ARN plus
// /policy. Policy is optional in the output while PutChannelPolicy requires
// one, so empty text is read as no policy attached and gets no row. The op
// models no error, so every error fails the channel (nil notFound).
func scanMTChannelPolicies(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, channels []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mtGetChannelPolicyOp, nil, channels,
		func(ctx context.Context, channel string, _ *string) ([]*mediatailor.GetChannelPolicyOutput, *string, error) {
			out, err := client.GetChannelPolicy(ctx, &mediatailor.GetChannelPolicyInput{ChannelName: &channel})
			if err != nil || sv(out.Policy) == "" {
				return nil, nil, err
			}
			return []*mediatailor.GetChannelPolicyOutput{out}, nil, nil
		},
		func(channel string, out *mediatailor.GetChannelPolicyOutput) (*store.Resource, string, string) {
			arn := arnFor(acct.partition(), "mediatailor", region, acct.ID, "channel/"+channel+"/policy")
			return mtRow(acct, region, scanID, TypeMediaTailorChannelPolicy, arn, &channel, out, nil), "", ""
		})
}

// scanMTPrograms stores the programs of each channel's schedule, each keyed by
// its own ARN. The op models no error, so every error fails the channel (nil
// notFound).
func scanMTPrograms(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, channels []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mtGetChannelScheduleOp, nil, channels,
		func(ctx context.Context, channel string, token *string) ([]mttypes.ScheduleEntry, *string, error) {
			out, err := client.GetChannelSchedule(ctx, &mediatailor.GetChannelScheduleInput{ChannelName: &channel, NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(channel string, p mttypes.ScheduleEntry) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "programs have an empty Arn (first names its channel)", channel
			}
			return mtRow(acct, region, scanID, TypeMediaTailorProgram, arn, p.ProgramName, p, nil), "", ""
		})
}

func scanMTSourceLocations(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	return storeMTParents(ctx, st, acct, region, mtListSourceLocationsOp, "source location(s)", "SourceLocationName",
		func(ctx context.Context, token *string) ([]mttypes.SourceLocation, *string, error) {
			out, err := client.ListSourceLocations(ctx, &mediatailor.ListSourceLocationsInput{NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(s mttypes.SourceLocation) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return nil, "source locations have an empty Arn, and their live and VOD sources were not read either", sv(s.SourceLocationName)
			}
			return mtRow(acct, region, scanID, TypeMediaTailorSourceLocation, arn, s.SourceLocationName, s, mapTagsJSON(s.Tags)), "", ""
		})
}

// scanMTLiveSources stores the live sources of each source location. The
// live, VOD and prefetch listings model no error, so none skips a code for a
// parent deleted since it was listed (nil notFound).
func scanMTLiveSources(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, locations []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mtListLiveSourcesOp, nil, locations,
		func(ctx context.Context, loc string, token *string) ([]mttypes.LiveSource, *string, error) {
			out, err := client.ListLiveSources(ctx, &mediatailor.ListLiveSourcesInput{SourceLocationName: &loc, NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(loc string, s mttypes.LiveSource) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return nil, "live sources have an empty Arn (first names its source location)", loc
			}
			return mtRow(acct, region, scanID, TypeMediaTailorLiveSource, arn, s.LiveSourceName, s, mapTagsJSON(s.Tags)), "", ""
		})
}

func scanMTVodSources(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, locations []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mtListVodSourcesOp, nil, locations,
		func(ctx context.Context, loc string, token *string) ([]mttypes.VodSource, *string, error) {
			out, err := client.ListVodSources(ctx, &mediatailor.ListVodSourcesInput{SourceLocationName: &loc, NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(loc string, s mttypes.VodSource) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return nil, "VOD sources have an empty Arn (first names its source location)", loc
			}
			return mtRow(acct, region, scanID, TypeMediaTailorVodSource, arn, s.VodSourceName, s, mapTagsJSON(s.Tags)), "", ""
		})
}

func scanMTPlaybackConfigurations(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	return storeMTParents(ctx, st, acct, region, mtListPlaybackConfigurationsOp, "playback configuration(s)", "Name",
		func(ctx context.Context, token *string) ([]mttypes.PlaybackConfiguration, *string, error) {
			out, err := client.ListPlaybackConfigurations(ctx, &mediatailor.ListPlaybackConfigurationsInput{NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(p mttypes.PlaybackConfiguration) (*store.Resource, string, string) {
			arn := sv(p.PlaybackConfigurationArn)
			if arn == "" {
				return nil, "playback configurations have an empty PlaybackConfigurationArn, and their prefetch schedules were not read either", sv(p.Name)
			}
			return mtRow(acct, region, scanID, TypeMediaTailorPlaybackConfiguration, arn, p.Name, p, mapTagsJSON(p.Tags)), "", ""
		})
}

// scanMTPrefetchSchedules stores the prefetch schedules of each playback
// configuration, each keyed by its own ARN. ScheduleType is ALL because an
// unset filter lists SINGLE schedules only (SDK CHANGELOG v1.47.0, when
// RECURRING schedules arrived).
func scanMTPrefetchSchedules(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, configs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, mtListPrefetchSchedulesOp, nil, configs,
		func(ctx context.Context, cfg string, token *string) ([]mttypes.PrefetchSchedule, *string, error) {
			out, err := client.ListPrefetchSchedules(ctx, &mediatailor.ListPrefetchSchedulesInput{PlaybackConfigurationName: &cfg, ScheduleType: mttypes.ListPrefetchScheduleTypeAll, NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(cfg string, p mttypes.PrefetchSchedule) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "prefetch schedules have an empty Arn (first names its playback configuration)", cfg
			}
			return mtRow(acct, region, scanID, TypeMediaTailorPrefetchSchedule, arn, p.Name, p, mapTagsJSON(p.Tags)), "", ""
		})
}

// scanMTFunctions stores every function in the region, keyed by the ARN the
// list returns. Function.Arn is optional in the model, unlike the ARN of the
// service's other list elements, so an element without one is not stored and
// the region gets one warning counting them.
//
// ListFunctions arrived in SDK v1.58.0, long after the rest of the MediaTailor
// API, so a region may serve playback configurations without it. On the first
// call, the shapes an unrouted operation takes (isOpNotDeployed) are a feature
// gap, skipped silently. ListFunctions models no error, so each arrives as a
// generic API error carrying the wire code or status. With no not-found error
// to model, a 404 on its unfiltered first call means the op is absent. Once a
// page has succeeded the op is deployed: a later error of any shape except a
// denial is returned, after the rows listed so far are stored.
func scanMTFunctions(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := false
	return storeTokenList(ctx, st, acct, region, mtListFunctionsOp,
		func(ctx context.Context, token *string) ([]mttypes.Function, *string, error) {
			out, err := client.ListFunctions(ctx, &mediatailor.ListFunctionsInput{NextToken: token, MaxResults: mtMaxResults})
			if err != nil {
				if !listed && isOpNotDeployed(err) {
					return nil, nil, nil
				}
				return nil, nil, err
			}
			listed = true
			return out.Items, out.NextToken, nil
		},
		func(f mttypes.Function) (*store.Resource, string, string) {
			arn := sv(f.Arn)
			if arn == "" {
				return nil, "functions have an empty Arn", sv(f.FunctionId)
			}
			return mtRow(acct, region, scanID, TypeMediaTailorFunction, arn, f.FunctionId, f, mapTagsJSON(f.Tags)), "", ""
		})
}
