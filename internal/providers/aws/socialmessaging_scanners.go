package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/socialmessaging"
	socialmessagingtypes "github.com/aws/aws-sdk-go-v2/service/socialmessaging/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:social-messaging",
		fn:   scanSocialMessaging,
		emits: []coverage.TypeDecl{
			{Service: "social-messaging", DiscoType: TypeSocialMessagingWaba, Leaf: true},
			{Service: "social-messaging", DiscoType: TypeSocialMessagingPhoneNumberID},
		},
	})
}

// socialMessagingPhoneNumberAttrs embeds the SDK phone-number summary and adds
// the parent WABA ARN, which the list/get response does not carry on each
// phone-number entry. The resolver reads WabaArn to wire the attached-to edge.
type socialMessagingPhoneNumberAttrs struct {
	socialmessagingtypes.WhatsAppPhoneNumberSummary
	WabaArn string `json:"WabaArn,omitempty"`
}

type socialMessagingAPI interface {
	ListLinkedWhatsAppBusinessAccounts(context.Context, *socialmessaging.ListLinkedWhatsAppBusinessAccountsInput, ...func(*socialmessaging.Options)) (*socialmessaging.ListLinkedWhatsAppBusinessAccountsOutput, error)
	GetLinkedWhatsAppBusinessAccount(context.Context, *socialmessaging.GetLinkedWhatsAppBusinessAccountInput, ...func(*socialmessaging.Options)) (*socialmessaging.GetLinkedWhatsAppBusinessAccountOutput, error)
}

// scanSocialMessaging discovers linked WhatsApp Business Accounts (WABAs) and,
// per WABA, the phone numbers registered to it. Phone numbers are only exposed
// on the per-WABA Get call, so the scanner fans out one Get per linked account.
func scanSocialMessaging(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := socialmessaging.NewFromConfig(acct.cfg, func(o *socialmessaging.Options) { o.Region = region })

	wabaIDs, t, i, ferr := scanSocialMessagingWABAs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, id := range wabaIDs {
		t, i, ferr = scanSocialMessagingPhoneNumbers(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanSocialMessagingWABAs(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := socialmessaging.NewListLinkedWhatsAppBusinessAccountsPaginator(client, &socialmessaging.ListLinkedWhatsAppBusinessAccountsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "social-messaging:ListLinkedWhatsAppBusinessAccounts", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("social-messaging:ListLinkedWhatsAppBusinessAccounts: %w", err)
		}
		for _, a := range out.LinkedAccounts {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			if id := sv(a.Id); id != "" {
				ids = append(ids, id)
			}
			status := string(a.RegistrationStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSocialMessagingWaba, NativeID: arn,
				Name: a.WabaName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "social-messaging wabas")
	return ids, t, i, err
}

func scanSocialMessagingPhoneNumbers(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID, wabaID string) (int, int, error) {
	id := wabaID
	out, err := client.GetLinkedWhatsAppBusinessAccount(ctx, &socialmessaging.GetLinkedWhatsAppBusinessAccountInput{Id: &id})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "social-messaging:GetLinkedWhatsAppBusinessAccount", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("social-messaging:GetLinkedWhatsAppBusinessAccount: %w", err)
	}
	if out.Account == nil {
		return 0, 0, nil
	}
	wabaArn := sv(out.Account.Arn)
	var batch []*store.Resource
	for _, p := range out.Account.PhoneNumbers {
		arn := sv(p.Arn)
		if arn == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSocialMessagingPhoneNumberID, NativeID: arn,
			Name: p.DisplayPhoneNumberName, Region: &region,
			AttributesJSON: mustJSON(socialMessagingPhoneNumberAttrs{WhatsAppPhoneNumberSummary: p, WabaArn: wabaArn}),
			DiscoveredBy:   scanID,
		})
	}
	return upsertBatch(st, batch, "social-messaging phone-numbers")
}
