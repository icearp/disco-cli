package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ivschat"
	ivschattypes "github.com/aws/aws-sdk-go-v2/service/ivschat/types"
	"github.com/aws/smithy-go"
)

const (
	ivsChatTaggedRoom     = "arn:aws:ivschat:us-east-1:123456789012:room/r1"
	ivsChatUntaggedRoom   = "arn:aws:ivschat:us-east-1:123456789012:room/r2"
	ivsChatTaggedLogging  = "arn:aws:ivschat:us-east-1:123456789012:logging-configuration/l1"
	ivsChatUntaggedLogCfg = "arn:aws:ivschat:us-east-1:123456789012:logging-configuration/l2"
)

func ivsChatTestClient() *ivschat.Client {
	return ivschat.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListRooms": func(any) (any, error) {
			return &ivschat.ListRoomsOutput{Rooms: []ivschattypes.RoomSummary{
				{Arn: sdkaws.String(ivsChatTaggedRoom), Name: sdkaws.String("r1"), Tags: map[string]string{"team": "live"}},
				{Arn: sdkaws.String(ivsChatUntaggedRoom), Name: sdkaws.String("r2")},
			}}, nil
		},
		"ListLoggingConfigurations": func(any) (any, error) {
			return &ivschat.ListLoggingConfigurationsOutput{LoggingConfigurations: []ivschattypes.LoggingConfigurationSummary{
				{Arn: sdkaws.String(ivsChatTaggedLogging), Name: sdkaws.String("l1"), Tags: map[string]string{"env": "prod"}},
				{Arn: sdkaws.String(ivsChatUntaggedLogCfg), Name: sdkaws.String("l2")},
			}}, nil
		},
	}, &smithy.GenericAPIError{Code: "AccessDeniedException"}))
}

func TestScanIVSChatRooms_ReadsListedTags(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := scanIVSChatRooms(context.Background(), ivsChatTestClient(), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeIVSChatRoom)
	assertTagsMap(t, rows[ivsChatTaggedRoom].TagsJSON, map[string]string{"team": "live"})
	if got := rows[ivsChatUntaggedRoom].TagsJSON; got != nil {
		t.Errorf("untagged room TagsJSON = %q, want nil", *got)
	}
}

func TestScanIVSChatLoggingConfigs_ReadsListedTags(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := scanIVSChatLoggingConfigs(context.Background(), ivsChatTestClient(), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeIVSChatLoggingConfiguration)
	assertTagsMap(t, rows[ivsChatTaggedLogging].TagsJSON, map[string]string{"env": "prod"})
	if got := rows[ivsChatUntaggedLogCfg].TagsJSON; got != nil {
		t.Errorf("untagged logging configuration TagsJSON = %q, want nil", *got)
	}
}
