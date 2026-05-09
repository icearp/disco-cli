package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
)

// scanPinpointChannels fans out per-app over all 10 Pinpoint channel
// types. Each channel is a singleton per (app, kind) — Get*Channel
// returns the channel config or NotFoundException when not configured
// for the app. NotFound is silently skipped, AccessDenied tolerated.
func scanPinpointChannels(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	if len(appIDs) == 0 {
		return 0, 0, nil
	}
	total := 0
	inserted := 0
	for _, fn := range []func(context.Context, pinpointAPI, *account, string, *store.Store, string, []string) (int, int, error){
		scanPinpointADM, scanPinpointAPNS, scanPinpointAPNSSandbox,
		scanPinpointAPNSVoip, scanPinpointAPNSVoipSandbox,
		scanPinpointBaidu, scanPinpointEmail, scanPinpointGCM,
		scanPinpointSMS, scanPinpointVoice,
	} {
		t, i, err := fn(ctx, client, acct, region, st, scanID, appIDs)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func pinpointChannelRow(acct *account, region, scanID, appID, kind, dtype string, body any) *store.Resource {
	arn := pinpointARN(region, acct.ID, "channels/"+kind, appID)
	name := kind
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: dtype, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(body), DiscoveredBy: scanID,
	}
}

func isPinpointNotFound(err error) bool {
	return isAPIErrorCode(err, "NotFoundException")
}

func scanPinpointADM(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetAdmChannel(ctx, &pinpoint.GetAdmChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetAdmChannel %s: %w", appID, err)
		}
		if out.ADMChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "adm", TypePinpointADMChannel, out.ADMChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint adm-channels")
}

func scanPinpointAPNS(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetApnsChannel(ctx, &pinpoint.GetApnsChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetApnsChannel %s: %w", appID, err)
		}
		if out.APNSChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "apns", TypePinpointAPNSChannel, out.APNSChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint apns-channels")
}

func scanPinpointAPNSSandbox(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetApnsSandboxChannel(ctx, &pinpoint.GetApnsSandboxChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetApnsSandboxChannel %s: %w", appID, err)
		}
		if out.APNSSandboxChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "apns_sandbox", TypePinpointAPNSSandboxChannel, out.APNSSandboxChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint apns-sandbox-channels")
}

func scanPinpointAPNSVoip(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetApnsVoipChannel(ctx, &pinpoint.GetApnsVoipChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetApnsVoipChannel %s: %w", appID, err)
		}
		if out.APNSVoipChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "apns_voip", TypePinpointAPNSVoipChannel, out.APNSVoipChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint apns-voip-channels")
}

func scanPinpointAPNSVoipSandbox(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetApnsVoipSandboxChannel(ctx, &pinpoint.GetApnsVoipSandboxChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetApnsVoipSandboxChannel %s: %w", appID, err)
		}
		if out.APNSVoipSandboxChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "apns_voip_sandbox", TypePinpointAPNSVoipSandboxChannel, out.APNSVoipSandboxChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint apns-voip-sandbox-channels")
}

func scanPinpointBaidu(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetBaiduChannel(ctx, &pinpoint.GetBaiduChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetBaiduChannel %s: %w", appID, err)
		}
		if out.BaiduChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "baidu", TypePinpointBaiduChannel, out.BaiduChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint baidu-channels")
}

func scanPinpointEmail(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetEmailChannel(ctx, &pinpoint.GetEmailChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetEmailChannel %s: %w", appID, err)
		}
		if out.EmailChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "email", TypePinpointEmailChannel, out.EmailChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint email-channels")
}

func scanPinpointGCM(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetGcmChannel(ctx, &pinpoint.GetGcmChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetGcmChannel %s: %w", appID, err)
		}
		if out.GCMChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "gcm", TypePinpointGCMChannel, out.GCMChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint gcm-channels")
}

func scanPinpointSMS(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetSmsChannel(ctx, &pinpoint.GetSmsChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetSmsChannel %s: %w", appID, err)
		}
		if out.SMSChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "sms", TypePinpointSMSChannel, out.SMSChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint sms-channels")
}

func scanPinpointVoice(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, err := client.GetVoiceChannel(ctx, &pinpoint.GetVoiceChannelInput{ApplicationId: &id})
		if err != nil {
			if isAccessDenied(err) || isPinpointNotFound(err) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetVoiceChannel %s: %w", appID, err)
		}
		if out.VoiceChannelResponse == nil {
			continue
		}
		batch = append(batch, pinpointChannelRow(acct, region, scanID, appID, "voice", TypePinpointVoiceChannel, out.VoiceChannelResponse))
	}
	return upsertBatch(st, batch, "pinpoint voice-channels")
}
