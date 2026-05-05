package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:sms-voice",
		fn:   scanSMSVoice,
		emits: []coverage.TypeDecl{
			{Service: "sms-voice", DiscoType: TypeSMSVoiceConfigurationSet},
			{Service: "sms-voice", DiscoType: TypeSMSVoiceOptOutList},
			{Service: "sms-voice", DiscoType: TypeSMSVoicePhoneNumber},
			{Service: "sms-voice", DiscoType: TypeSMSVoicePool},
			{Service: "sms-voice", DiscoType: TypeSMSVoiceProtectConfiguration},
			{Service: "sms-voice", DiscoType: TypeSMSVoiceSenderID},
		},
	})
}

type smsVoiceAPI interface {
	DescribeConfigurationSets(context.Context, *pinpointsmsvoicev2.DescribeConfigurationSetsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeConfigurationSetsOutput, error)
	DescribeOptOutLists(context.Context, *pinpointsmsvoicev2.DescribeOptOutListsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeOptOutListsOutput, error)
	DescribePhoneNumbers(context.Context, *pinpointsmsvoicev2.DescribePhoneNumbersInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribePhoneNumbersOutput, error)
	DescribePools(context.Context, *pinpointsmsvoicev2.DescribePoolsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribePoolsOutput, error)
	DescribeProtectConfigurations(context.Context, *pinpointsmsvoicev2.DescribeProtectConfigurationsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeProtectConfigurationsOutput, error)
	DescribeSenderIds(context.Context, *pinpointsmsvoicev2.DescribeSenderIdsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeSenderIdsOutput, error)
}

func scanSMSVoice(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pinpointsmsvoicev2.NewFromConfig(acct.cfg, func(o *pinpointsmsvoicev2.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSVConfigurationSets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSVOptOutLists(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSVPhoneNumbers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSVPools(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSVProtectConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSVSenderIDs(ctx, client, acct, region, st, scanID) },
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

func scanSVConfigurationSets(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointsmsvoicev2.NewDescribeConfigurationSetsPaginator(client, &pinpointsmsvoicev2.DescribeConfigurationSetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sms-voice:DescribeConfigurationSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sms-voice:DescribeConfigurationSets: %w", perr)
		}
		for _, c := range out.ConfigurationSets {
			arn := sv(c.ConfigurationSetArn)
			if arn == "" {
				continue
			}
			label := sv(c.ConfigurationSetName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSMSVoiceConfigurationSet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sms-voice configuration-sets")
}

func scanSVOptOutLists(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointsmsvoicev2.NewDescribeOptOutListsPaginator(client, &pinpointsmsvoicev2.DescribeOptOutListsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sms-voice:DescribeOptOutLists", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sms-voice:DescribeOptOutLists: %w", perr)
		}
		for _, l := range out.OptOutLists {
			arn := sv(l.OptOutListArn)
			if arn == "" {
				continue
			}
			label := sv(l.OptOutListName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSMSVoiceOptOutList, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
				// OptOutListName "Default" identifies the AWS-managed default
				// opt-out list present in every account.
				ManagedByProvider: sv(l.OptOutListName) == "Default",
			})
		}
	}
	return upsertBatch(st, batch, "sms-voice opt-out-lists")
}

func scanSVPhoneNumbers(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointsmsvoicev2.NewDescribePhoneNumbersPaginator(client, &pinpointsmsvoicev2.DescribePhoneNumbersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sms-voice:DescribePhoneNumbers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sms-voice:DescribePhoneNumbers: %w", perr)
		}
		for _, p := range out.PhoneNumbers {
			arn := sv(p.PhoneNumberArn)
			if arn == "" {
				continue
			}
			label := sv(p.PhoneNumber)
			if label == "" {
				label = sv(p.PhoneNumberId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSMSVoicePhoneNumber, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sms-voice phone-numbers")
}

func scanSVPools(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointsmsvoicev2.NewDescribePoolsPaginator(client, &pinpointsmsvoicev2.DescribePoolsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sms-voice:DescribePools", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sms-voice:DescribePools: %w", perr)
		}
		for _, p := range out.Pools {
			arn := sv(p.PoolArn)
			if arn == "" {
				continue
			}
			label := sv(p.PoolId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSMSVoicePool, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sms-voice pools")
}

func scanSVProtectConfigurations(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointsmsvoicev2.NewDescribeProtectConfigurationsPaginator(client, &pinpointsmsvoicev2.DescribeProtectConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sms-voice:DescribeProtectConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sms-voice:DescribeProtectConfigurations: %w", perr)
		}
		for _, p := range out.ProtectConfigurations {
			arn := sv(p.ProtectConfigurationArn)
			if arn == "" {
				continue
			}
			label := sv(p.ProtectConfigurationId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSMSVoiceProtectConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sms-voice protect-configurations")
}

func scanSVSenderIDs(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointsmsvoicev2.NewDescribeSenderIdsPaginator(client, &pinpointsmsvoicev2.DescribeSenderIdsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sms-voice:DescribeSenderIds", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sms-voice:DescribeSenderIds: %w", perr)
		}
		for _, s := range out.SenderIds {
			arn := sv(s.SenderIdArn)
			if arn == "" {
				continue
			}
			label := sv(s.SenderId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSMSVoiceSenderID, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sms-voice sender-ids")
}
