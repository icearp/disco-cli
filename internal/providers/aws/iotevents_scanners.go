package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iotevents"
)

func init() {
	registerService(serviceEntry{
		name: "aws:iot-events",
		fn:   scanIoTEvents,
		emits: []coverage.TypeDecl{
			{Service: "iot-events", DiscoType: TypeIoTEventsAlarmModel},
			{Service: "iot-events", DiscoType: TypeIoTEventsDetectorModel},
			{Service: "iot-events", DiscoType: TypeIoTEventsInput},
		},
	})
}

type ioTEventsAPI interface {
	ListAlarmModels(context.Context, *iotevents.ListAlarmModelsInput, ...func(*iotevents.Options)) (*iotevents.ListAlarmModelsOutput, error)
	ListDetectorModels(context.Context, *iotevents.ListDetectorModelsInput, ...func(*iotevents.Options)) (*iotevents.ListDetectorModelsOutput, error)
	ListInputs(context.Context, *iotevents.ListInputsInput, ...func(*iotevents.Options)) (*iotevents.ListInputsOutput, error)
}

// scanIoTEvents discovers IoT Events alarm models, detector models, and
// inputs. List APIs use manual NextToken loops since the SDK exposes no
// paginators. AlarmModel/DetectorModel summaries lack ARNs — synthesize.
func scanIoTEvents(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotevents.NewFromConfig(acct.cfg, func(o *iotevents.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIETAlarmModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIETDetectorModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIETInputs(ctx, client, acct, region, st, scanID) },
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

func scanIETAlarmModels(ctx context.Context, client ioTEventsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListAlarmModels(ctx, &iotevents.ListAlarmModelsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotevents:ListAlarmModels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotevents:ListAlarmModels: %w", err)
		}
		for _, a := range out.AlarmModelSummaries {
			name := sv(a.AlarmModelName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:iotevents:%s:%s:alarmModel/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTEventsAlarmModel, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotevents alarm-models")
}

func scanIETDetectorModels(ctx context.Context, client ioTEventsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDetectorModels(ctx, &iotevents.ListDetectorModelsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotevents:ListDetectorModels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotevents:ListDetectorModels: %w", err)
		}
		for _, d := range out.DetectorModelSummaries {
			name := sv(d.DetectorModelName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:iotevents:%s:%s:detectorModel/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTEventsDetectorModel, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotevents detector-models")
}

func scanIETInputs(ctx context.Context, client ioTEventsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListInputs(ctx, &iotevents.ListInputsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotevents:ListInputs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotevents:ListInputs: %w", err)
		}
		for _, i := range out.InputSummaries {
			arn := sv(i.InputArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTEventsInput, NativeID: arn,
				Name: i.InputName, Region: &region,
				AttributesJSON: mustJSON(i), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotevents inputs")
}
