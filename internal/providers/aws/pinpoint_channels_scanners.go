package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
	pptypes "github.com/aws/aws-sdk-go-v2/service/pinpoint/types"
)

// pinpointChannelPhases returns one phase per Pinpoint channel type. Each
// channel is a per-app singleton read for every app concurrently and stored
// under NativeID apps/{appId}/channels/{kind}, named kind.
func pinpointChannelPhases(ctx context.Context, client pinpointAPI, apps pinpointApps) []pinpointPhase {
	return []pinpointPhase{
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetAdmChannel", TypePinpointADMChannel, "channels/adm", "adm",
			func(ctx context.Context, id string) (*pptypes.ADMChannelResponse, error) {
				out, err := client.GetAdmChannel(ctx, &pinpoint.GetAdmChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.ADMChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetApnsChannel", TypePinpointAPNSChannel, "channels/apns", "apns",
			func(ctx context.Context, id string) (*pptypes.APNSChannelResponse, error) {
				out, err := client.GetApnsChannel(ctx, &pinpoint.GetApnsChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.APNSChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetApnsSandboxChannel", TypePinpointAPNSSandboxChannel, "channels/apns_sandbox", "apns_sandbox",
			func(ctx context.Context, id string) (*pptypes.APNSSandboxChannelResponse, error) {
				out, err := client.GetApnsSandboxChannel(ctx, &pinpoint.GetApnsSandboxChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.APNSSandboxChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetApnsVoipChannel", TypePinpointAPNSVoipChannel, "channels/apns_voip", "apns_voip",
			func(ctx context.Context, id string) (*pptypes.APNSVoipChannelResponse, error) {
				out, err := client.GetApnsVoipChannel(ctx, &pinpoint.GetApnsVoipChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.APNSVoipChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetApnsVoipSandboxChannel", TypePinpointAPNSVoipSandboxChannel, "channels/apns_voip_sandbox", "apns_voip_sandbox",
			func(ctx context.Context, id string) (*pptypes.APNSVoipSandboxChannelResponse, error) {
				out, err := client.GetApnsVoipSandboxChannel(ctx, &pinpoint.GetApnsVoipSandboxChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.APNSVoipSandboxChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetBaiduChannel", TypePinpointBaiduChannel, "channels/baidu", "baidu",
			func(ctx context.Context, id string) (*pptypes.BaiduChannelResponse, error) {
				out, err := client.GetBaiduChannel(ctx, &pinpoint.GetBaiduChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.BaiduChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetEmailChannel", TypePinpointEmailChannel, "channels/email", "email",
			func(ctx context.Context, id string) (*pptypes.EmailChannelResponse, error) {
				out, err := client.GetEmailChannel(ctx, &pinpoint.GetEmailChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.EmailChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetGcmChannel", TypePinpointGCMChannel, "channels/gcm", "gcm",
			func(ctx context.Context, id string) (*pptypes.GCMChannelResponse, error) {
				out, err := client.GetGcmChannel(ctx, &pinpoint.GetGcmChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.GCMChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetSmsChannel", TypePinpointSMSChannel, "channels/sms", "sms",
			func(ctx context.Context, id string) (*pptypes.SMSChannelResponse, error) {
				out, err := client.GetSmsChannel(ctx, &pinpoint.GetSmsChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.SMSChannelResponse, nil
			}),
		pinpointPerAppPhase(ctx, apps, "pinpoint:GetVoiceChannel", TypePinpointVoiceChannel, "channels/voice", "voice",
			func(ctx context.Context, id string) (*pptypes.VoiceChannelResponse, error) {
				out, err := client.GetVoiceChannel(ctx, &pinpoint.GetVoiceChannelInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.VoiceChannelResponse, nil
			}),
	}
}
