package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ivschat"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIVSChatRoom, Service: "ivs-chat", Upstream: "AWS::IVSChat::Room", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIVSChatLoggingConfiguration, Service: "ivs-chat", Upstream: "AWS::IVSChat::LoggingConfiguration", Leaf: true})
	registerService(serviceEntry{
		name: "aws:ivs-chat",
		fn:   scanIVSChat,
	})
}

type ivsChatAPI interface {
	ListRooms(context.Context, *ivschat.ListRoomsInput, ...func(*ivschat.Options)) (*ivschat.ListRoomsOutput, error)
	ListLoggingConfigurations(context.Context, *ivschat.ListLoggingConfigurationsInput, ...func(*ivschat.Options)) (*ivschat.ListLoggingConfigurationsOutput, error)
}

// scanIVSChat discovers IVS Chat rooms and logging configurations.
func scanIVSChat(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ivschat.NewFromConfig(acct.cfg, func(o *ivschat.Options) { o.Region = region })

	t, i, ferr := scanIVSChatRooms(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanIVSChatLoggingConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanIVSChatRooms(ctx context.Context, client ivsChatAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListRooms(ctx, &ivschat.ListRoomsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ivschat:ListRooms", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ivschat:ListRooms: %w", err)
		}
		for _, r := range out.Rooms {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSChatRoom, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ivs-chat rooms")
}

func scanIVSChatLoggingConfigs(ctx context.Context, client ivsChatAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLoggingConfigurations(ctx, &ivschat.ListLoggingConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ivschat:ListLoggingConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ivschat:ListLoggingConfigurations: %w", err)
		}
		for _, l := range out.LoggingConfigurations {
			arn := sv(l.Arn)
			if arn == "" {
				continue
			}
			status := string(l.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIVSChatLoggingConfiguration, NativeID: arn,
				Name: l.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ivs-chat logging-configurations")
}
