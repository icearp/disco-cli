package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/mediatailor"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaTailorChannel, Service: "mediatailor", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMediaTailorChannelPolicy, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorLiveSource, Service: "mediatailor"})
	registerType(restype.Descriptor{Type: TypeMediaTailorPlaybackConfiguration, Service: "mediatailor", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMediaTailorPrefetchSchedule, Service: "mediatailor", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMediaTailorProgram, Service: "mediatailor", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMediaTailorSourceLocation, Service: "mediatailor", Leaf: true})
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
}

// scanMediaTailor discovers MediaTailor channels (with per-channel policies)
// and source locations (with per-location live + VOD sources), plus
// account-wide playback configurations.
func scanMediaTailor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mediatailor.NewFromConfig(acct.cfg, func(o *mediatailor.Options) { o.Region = region })

	channelNames, t, i, ferr := scanMTChannels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, cn := range channelNames {
		t, i, ferr = scanMTChannelPolicy(ctx, client, acct, region, st, scanID, cn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanMTPrograms(ctx, client, acct, region, st, scanID, cn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	locNames, t, i, ferr := scanMTSourceLocations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, ln := range locNames {
		t, i, ferr = scanMTLiveSources(ctx, client, acct, region, st, scanID, ln)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanMTVodSources(ctx, client, acct, region, st, scanID, ln)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	pcNames, t, i, ferr := scanMTPlaybackConfigurations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, pcn := range pcNames {
		t, i, ferr = scanMTPrefetchSchedules(ctx, client, acct, region, st, scanID, pcn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanMTChannels(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediatailor.NewListChannelsPaginator(client, &mediatailor.ListChannelsInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mediatailor:ListChannels", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mediatailor:ListChannels: %w", err)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			if n := sv(c.ChannelName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorChannel, NativeID: arn,
				Name: c.ChannelName, Region: &region, Status: c.ChannelState,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediatailor channels")
	return names, t, i, err
}

// scanMTChannelPolicy fetches the per-channel IAM policy. API returns only
// a Policy string; synthesize ARN: arn:aws:mediatailor:{r}:{a}:channel/{name}/policy.
func scanMTChannelPolicy(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, channelName string) (int, int, error) {
	cn := channelName
	out, err := client.GetChannelPolicy(ctx, &mediatailor.GetChannelPolicyInput{ChannelName: &cn})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "NotFoundException", "BadRequestException") {
			// No policy attached for this channel.
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("mediatailor:GetChannelPolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:mediatailor:%s:%s:channel/%s/policy", region, acct.ID, cn)
	label := cn
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeMediaTailorChannelPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "mediatailor channel-policies")
}

func scanMTSourceLocations(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediatailor.NewListSourceLocationsPaginator(client, &mediatailor.ListSourceLocationsInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mediatailor:ListSourceLocations", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mediatailor:ListSourceLocations: %w", err)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			if n := sv(s.SourceLocationName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorSourceLocation, NativeID: arn,
				Name: s.SourceLocationName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediatailor source-locations")
	return names, t, i, err
}

func scanMTLiveSources(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, locName string) (int, int, error) {
	ln := locName
	pager := mediatailor.NewListLiveSourcesPaginator(client, &mediatailor.ListLiveSourcesInput{SourceLocationName: &ln})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediatailor:ListLiveSources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediatailor:ListLiveSources: %w", err)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorLiveSource, NativeID: arn,
				Name: s.LiveSourceName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediatailor live-sources")
}

func scanMTVodSources(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, locName string) (int, int, error) {
	ln := locName
	pager := mediatailor.NewListVodSourcesPaginator(client, &mediatailor.ListVodSourcesInput{SourceLocationName: &ln})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediatailor:ListVodSources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediatailor:ListVodSources: %w", err)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorVodSource, NativeID: arn,
				Name: s.VodSourceName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediatailor vod-sources")
}

func scanMTPlaybackConfigurations(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediatailor.NewListPlaybackConfigurationsPaginator(client, &mediatailor.ListPlaybackConfigurationsInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mediatailor:ListPlaybackConfigurations", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mediatailor:ListPlaybackConfigurations: %w", err)
		}
		for _, p := range out.Items {
			arn := sv(p.PlaybackConfigurationArn)
			if arn == "" {
				continue
			}
			if n := sv(p.Name); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorPlaybackConfiguration, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediatailor playback-configurations")
	return names, t, i, err
}

// scanMTPrefetchSchedules — ListPrefetchSchedules requires
// PlaybackConfigurationName; fan out over scanned playback configs.
// PrefetchSchedule carries a real distinct ARN (Leaf).
func scanMTPrefetchSchedules(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, pcName string) (int, int, error) {
	pcn := pcName
	pager := mediatailor.NewListPrefetchSchedulesPaginator(client, &mediatailor.ListPrefetchSchedulesInput{PlaybackConfigurationName: &pcn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediatailor:ListPrefetchSchedules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediatailor:ListPrefetchSchedules: %w", err)
		}
		for _, p := range out.Items {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorPrefetchSchedule, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediatailor prefetch-schedules")
}

// scanMTPrograms — GetChannelSchedule requires ChannelName; fan out over
// scanned channels. Each ScheduleEntry is a program carrying a real distinct
// ARN (Leaf).
func scanMTPrograms(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string, channelName string) (int, int, error) {
	cn := channelName
	pager := mediatailor.NewGetChannelSchedulePaginator(client, &mediatailor.GetChannelScheduleInput{ChannelName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "NotFoundException", "BadRequestException") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mediatailor:GetChannelSchedule: %w", err)
		}
		for _, p := range out.Items {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorProgram, NativeID: arn,
				Name: p.ProgramName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediatailor programs")
}
