package aws

import (
	"cmp"
	"context"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	svtypes "github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSMSVoiceConfigurationSet, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceOptOutList, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoicePhoneNumber, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoicePool, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceProtectConfiguration, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceSenderID, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceRegistration, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceRegistrationAttachment, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceVerifiedDestinationNumber, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceNotifyConfiguration, Service: "sms-voice"})
	registerType(restype.Descriptor{Type: TypeSMSVoiceRcsAgent, Service: "sms-voice"})
	registerService(serviceEntry{
		name: "aws:sms-voice",
		fn:   scanSMSVoice,
	})
}

type smsVoiceAPI interface {
	DescribeConfigurationSets(context.Context, *pinpointsmsvoicev2.DescribeConfigurationSetsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeConfigurationSetsOutput, error)
	DescribeOptOutLists(context.Context, *pinpointsmsvoicev2.DescribeOptOutListsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeOptOutListsOutput, error)
	DescribePhoneNumbers(context.Context, *pinpointsmsvoicev2.DescribePhoneNumbersInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribePhoneNumbersOutput, error)
	DescribePools(context.Context, *pinpointsmsvoicev2.DescribePoolsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribePoolsOutput, error)
	DescribeProtectConfigurations(context.Context, *pinpointsmsvoicev2.DescribeProtectConfigurationsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeProtectConfigurationsOutput, error)
	DescribeSenderIds(context.Context, *pinpointsmsvoicev2.DescribeSenderIdsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeSenderIdsOutput, error)
	DescribeRegistrations(context.Context, *pinpointsmsvoicev2.DescribeRegistrationsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeRegistrationsOutput, error)
	DescribeRegistrationAttachments(context.Context, *pinpointsmsvoicev2.DescribeRegistrationAttachmentsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeRegistrationAttachmentsOutput, error)
	DescribeVerifiedDestinationNumbers(context.Context, *pinpointsmsvoicev2.DescribeVerifiedDestinationNumbersInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeVerifiedDestinationNumbersOutput, error)
	DescribeNotifyConfigurations(context.Context, *pinpointsmsvoicev2.DescribeNotifyConfigurationsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeNotifyConfigurationsOutput, error)
	DescribeRcsAgents(context.Context, *pinpointsmsvoicev2.DescribeRcsAgentsInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.DescribeRcsAgentsOutput, error)
	ListTagsForResource(context.Context, *pinpointsmsvoicev2.ListTagsForResourceInput, ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.ListTagsForResourceOutput, error)
}

// svPageCap is the maximum of the MaxResults range trait (1-100) of every
// Describe op this scanner calls.
const svPageCap int32 = 100

// svTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned. The Service Reference
// lists every type this scanner stores as a resource of the op, and the op
// returns every tag in one response (its output has no NextToken).
func svTags(client smsVoiceAPI) rowTagReader {
	return rowTagReader{
		op:       "sms-voice:ListTagsForResource",
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &pinpointsmsvoicev2.ListTagsForResourceInput{ResourceArn: &r.NativeID})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t svtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// svSharedTags is svTags for the types whose Describe op takes an Owner
// filter (SELF or SHARED: "shared with your account"): opt-out lists, phone
// numbers, pools, sender ids and RCS agents. The scanner sends no Owner, and
// the op's doc does not say which owners an unfiltered listing returns, so a
// listed row's ARN may carry the sharing account. This account may not be
// able to read another account's resource, so for such a row neither a
// denial nor a not-found answer means it is gone: the row is kept without
// tags and without a warning. Any other error is returned. An own-account row
// keeps the usual rules: not-found drops it, a denial warns.
func svSharedTags(tags rowTagReader, acct *account) rowTagReader {
	own := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := own(ctx, r)
		if a, ok := parseARN(r.NativeID); ok && a.AccountID != acct.ID && (isAccessDenied(err) || isAPIErrorCode(err, tags.notFound...)) {
			return nil, nil
		}
		return got, err
	}
	return tags
}

// scanSMSVoice discovers the End User Messaging SMS and Voice v2 resources,
// each with its tags. Every listing runs as its own phase: a phase's error
// does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The phases run one at a time, so readEachKey's own bound
// is the bound on the tag reads in flight.
func scanSMSVoice(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := pinpointsmsvoicev2.NewFromConfig(acct.cfg, func(o *pinpointsmsvoicev2.Options) { o.Region = region })
	tags := svTags(client)
	shared := svSharedTags(tags, acct)
	return runHostPhases(ctx, st, acct, region, "sms-voice", 0, []hostPhase{
		{op: "sms-voice:DescribeConfigurationSets", run: func(p *phaseTotals) bool {
			return scanSVConfigurationSets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "sms-voice:DescribeOptOutLists", run: func(p *phaseTotals) bool {
			return scanSVOptOutLists(ctx, client, acct, region, st, scanID, shared, p)
		}},
		{op: "sms-voice:DescribePhoneNumbers", run: func(p *phaseTotals) bool {
			return scanSVPhoneNumbers(ctx, client, acct, region, st, scanID, shared, p)
		}},
		{op: "sms-voice:DescribePools", run: func(p *phaseTotals) bool {
			return scanSVPools(ctx, client, acct, region, st, scanID, shared, p)
		}},
		{op: "sms-voice:DescribeProtectConfigurations", run: func(p *phaseTotals) bool {
			return scanSVProtectConfigurations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "sms-voice:DescribeSenderIds", run: func(p *phaseTotals) bool {
			return scanSVSenderIDs(ctx, client, acct, region, st, scanID, shared, p)
		}},
		{op: "sms-voice:DescribeRegistrations", run: func(p *phaseTotals) bool {
			return scanSVRegistrations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "sms-voice:DescribeRegistrationAttachments", run: func(p *phaseTotals) bool {
			return scanSVRegistrationAttachments(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "sms-voice:DescribeVerifiedDestinationNumbers", run: func(p *phaseTotals) bool {
			return scanSVVerifiedDestinationNumbers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "sms-voice:DescribeNotifyConfigurations", run: func(p *phaseTotals) bool {
			return scanSVNotifyConfigurations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "sms-voice:DescribeRcsAgents", run: func(p *phaseTotals) bool {
			return scanSVRcsAgents(ctx, client, acct, region, st, scanID, shared, p)
		}},
	})
}

// svRow is the row of a listed element whose NativeID is its ARN. An element
// without an ARN is not stored; what names the elements in the warning and
// key the element.
func svRow(acct *account, region, scanID, typ, what, arn, name, key string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(key, "(an element with no identifier)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// svRowWithState is svRow for the elements that carry a status and a
// creation time; an empty status stores none.
func svRowWithState(acct *account, region, scanID, typ, what, arn, name, key, status string, created *time.Time, item any) (*store.Resource, string, string) {
	r, why, k := svRow(acct, region, scanID, typ, what, arn, name, key, item)
	if r != nil {
		r.Status, r.CreatedAt = nonEmptyPtr(status), tp(created)
	}
	return r, why, k
}

func scanSVConfigurationSets(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeConfigurationSets",
		func(ctx context.Context, token *string) ([]svtypes.ConfigurationSetInformation, *string, error) {
			out, err := client.DescribeConfigurationSets(ctx, &pinpointsmsvoicev2.DescribeConfigurationSetsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfigurationSets, out.NextToken, nil
		},
		func(c svtypes.ConfigurationSetInformation) (*store.Resource, string, string) {
			arn := sv(c.ConfigurationSetArn)
			return svRow(acct, region, scanID, TypeSMSVoiceConfigurationSet, "configuration sets", arn, cmp.Or(sv(c.ConfigurationSetName), arn), sv(c.ConfigurationSetName), c)
		}, tags, p)
}

func scanSVOptOutLists(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeOptOutLists",
		func(ctx context.Context, token *string) ([]svtypes.OptOutListInformation, *string, error) {
			out, err := client.DescribeOptOutLists(ctx, &pinpointsmsvoicev2.DescribeOptOutListsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.OptOutLists, out.NextToken, nil
		},
		func(l svtypes.OptOutListInformation) (*store.Resource, string, string) {
			arn := sv(l.OptOutListArn)
			return svRow(acct, region, scanID, TypeSMSVoiceOptOutList, "opt-out lists", arn, cmp.Or(sv(l.OptOutListName), arn), sv(l.OptOutListName), l)
		}, tags, p)
}

func scanSVPhoneNumbers(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribePhoneNumbers",
		func(ctx context.Context, token *string) ([]svtypes.PhoneNumberInformation, *string, error) {
			out, err := client.DescribePhoneNumbers(ctx, &pinpointsmsvoicev2.DescribePhoneNumbersInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PhoneNumbers, out.NextToken, nil
		},
		func(n svtypes.PhoneNumberInformation) (*store.Resource, string, string) {
			return svRow(acct, region, scanID, TypeSMSVoicePhoneNumber, "phone numbers", sv(n.PhoneNumberArn), cmp.Or(sv(n.PhoneNumber), sv(n.PhoneNumberId)), sv(n.PhoneNumberId), n)
		}, tags, p)
}

func scanSVPools(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribePools",
		func(ctx context.Context, token *string) ([]svtypes.PoolInformation, *string, error) {
			out, err := client.DescribePools(ctx, &pinpointsmsvoicev2.DescribePoolsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Pools, out.NextToken, nil
		},
		func(l svtypes.PoolInformation) (*store.Resource, string, string) {
			arn := sv(l.PoolArn)
			return svRow(acct, region, scanID, TypeSMSVoicePool, "pools", arn, cmp.Or(sv(l.PoolId), arn), sv(l.PoolId), l)
		}, tags, p)
}

func scanSVProtectConfigurations(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeProtectConfigurations",
		func(ctx context.Context, token *string) ([]svtypes.ProtectConfigurationInformation, *string, error) {
			out, err := client.DescribeProtectConfigurations(ctx, &pinpointsmsvoicev2.DescribeProtectConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ProtectConfigurations, out.NextToken, nil
		},
		func(c svtypes.ProtectConfigurationInformation) (*store.Resource, string, string) {
			arn := sv(c.ProtectConfigurationArn)
			return svRow(acct, region, scanID, TypeSMSVoiceProtectConfiguration, "protect configurations", arn, cmp.Or(sv(c.ProtectConfigurationId), arn), sv(c.ProtectConfigurationId), c)
		}, tags, p)
}

func scanSVSenderIDs(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeSenderIds",
		func(ctx context.Context, token *string) ([]svtypes.SenderIdInformation, *string, error) {
			out, err := client.DescribeSenderIds(ctx, &pinpointsmsvoicev2.DescribeSenderIdsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.SenderIds, out.NextToken, nil
		},
		func(s svtypes.SenderIdInformation) (*store.Resource, string, string) {
			arn := sv(s.SenderIdArn)
			return svRow(acct, region, scanID, TypeSMSVoiceSenderID, "sender ids", arn, cmp.Or(sv(s.SenderId), arn), sv(s.SenderId), s)
		}, tags, p)
}

func scanSVRegistrations(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeRegistrations",
		func(ctx context.Context, token *string) ([]svtypes.RegistrationInformation, *string, error) {
			out, err := client.DescribeRegistrations(ctx, &pinpointsmsvoicev2.DescribeRegistrationsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Registrations, out.NextToken, nil
		},
		func(r svtypes.RegistrationInformation) (*store.Resource, string, string) {
			arn := sv(r.RegistrationArn)
			return svRow(acct, region, scanID, TypeSMSVoiceRegistration, "registrations", arn, cmp.Or(sv(r.RegistrationId), arn), sv(r.RegistrationId), r)
		}, tags, p)
}

func scanSVRegistrationAttachments(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeRegistrationAttachments",
		func(ctx context.Context, token *string) ([]svtypes.RegistrationAttachmentsInformation, *string, error) {
			out, err := client.DescribeRegistrationAttachments(ctx, &pinpointsmsvoicev2.DescribeRegistrationAttachmentsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RegistrationAttachments, out.NextToken, nil
		},
		func(a svtypes.RegistrationAttachmentsInformation) (*store.Resource, string, string) {
			arn := sv(a.RegistrationAttachmentArn)
			return svRow(acct, region, scanID, TypeSMSVoiceRegistrationAttachment, "registration attachments", arn, cmp.Or(sv(a.RegistrationAttachmentId), arn), sv(a.RegistrationAttachmentId), a)
		}, tags, p)
}

func scanSVVerifiedDestinationNumbers(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeVerifiedDestinationNumbers",
		func(ctx context.Context, token *string) ([]svtypes.VerifiedDestinationNumberInformation, *string, error) {
			out, err := client.DescribeVerifiedDestinationNumbers(ctx, &pinpointsmsvoicev2.DescribeVerifiedDestinationNumbersInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.VerifiedDestinationNumbers, out.NextToken, nil
		},
		func(n svtypes.VerifiedDestinationNumberInformation) (*store.Resource, string, string) {
			return svRow(acct, region, scanID, TypeSMSVoiceVerifiedDestinationNumber, "verified destination numbers", sv(n.VerifiedDestinationNumberArn), cmp.Or(sv(n.DestinationPhoneNumber), sv(n.VerifiedDestinationNumberId)), sv(n.VerifiedDestinationNumberId), n)
		}, tags, p)
}

func scanSVNotifyConfigurations(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeNotifyConfigurations",
		func(ctx context.Context, token *string) ([]svtypes.NotifyConfigurationInformation, *string, error) {
			out, err := client.DescribeNotifyConfigurations(ctx, &pinpointsmsvoicev2.DescribeNotifyConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.NotifyConfigurations, out.NextToken, nil
		},
		func(c svtypes.NotifyConfigurationInformation) (*store.Resource, string, string) {
			return svRowWithState(acct, region, scanID, TypeSMSVoiceNotifyConfiguration, "notify configurations", sv(c.NotifyConfigurationArn),
				cmp.Or(sv(c.DisplayName), sv(c.NotifyConfigurationId)), sv(c.NotifyConfigurationId), string(c.Status), c.CreatedTimestamp, c)
		}, tags, p)
}

func scanSVRcsAgents(ctx context.Context, client smsVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "sms-voice:DescribeRcsAgents",
		func(ctx context.Context, token *string) ([]svtypes.RcsAgentInformation, *string, error) {
			out, err := client.DescribeRcsAgents(ctx, &pinpointsmsvoicev2.DescribeRcsAgentsInput{NextToken: token, MaxResults: sdkaws.Int32(svPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RcsAgents, out.NextToken, nil
		},
		func(a svtypes.RcsAgentInformation) (*store.Resource, string, string) {
			arn := sv(a.RcsAgentArn)
			return svRowWithState(acct, region, scanID, TypeSMSVoiceRcsAgent, "RCS agents", arn,
				cmp.Or(sv(a.RcsAgentId), arn), sv(a.RcsAgentId), string(a.Status), a.CreatedTimestamp, a)
		}, tags, p)
}
