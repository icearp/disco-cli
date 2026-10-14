package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/mediatailor"
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

// scanMediaTailor runs the media phase and the function phase. Neither reads
// the other's output, so an error in one does not cost the other's rows.
func scanMediaTailor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mediatailor.NewFromConfig(acct.cfg, func(o *mediatailor.Options) { o.Region = region })
	var errs []error
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanMTMedia(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMTFunctions(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

// scanMTMedia discovers MediaTailor channels (with per-channel policies)
// and source locations (with per-location live + VOD sources), plus
// account-wide playback configurations.
func scanMTMedia(ctx context.Context, client mediatailorAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
// PrefetchSchedule carries a real distinct ARN (edge-less).
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
// ARN (edge-less).
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
	const op = "mediatailor:ListFunctions"
	// The model caps MaxResults at 100.
	pager := mediatailor.NewListFunctionsPaginator(client, &mediatailor.ListFunctionsInput{},
		func(o *mediatailor.ListFunctionsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	var listErr error
	listed := false
	noARN := 0
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			switch {
			case !listed && isOpNotDeployed(perr):
				return 0, 0, nil
			case isAccessDenied(perr):
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			default:
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		listed = true
		for _, f := range out.Items {
			arn := sv(f.Arn)
			if arn == "" {
				noARN++
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaTailorFunction, NativeID: arn,
				Name: f.FunctionId, Region: &region,
				AttributesJSON: mustJSON(f), TagsJSON: mapTagsJSON(f.Tags), DiscoveredBy: scanID,
			})
		}
	}
	if noARN > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d functions listed without an ARN were not stored", noARN),
		})
	}
	t, i, uerr := upsertBatch(st, batch, "mediatailor functions")
	return t, i, errors.Join(listErr, uerr)
}
