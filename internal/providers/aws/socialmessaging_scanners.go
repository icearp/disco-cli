package aws

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/socialmessaging"
	socialmessagingtypes "github.com/aws/aws-sdk-go-v2/service/socialmessaging/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSocialMessagingWaba, Service: "social-messaging"})
	registerType(restype.Descriptor{Type: TypeSocialMessagingPhoneNumberID, Service: "social-messaging"})
	registerType(restype.Descriptor{Type: TypeSocialMessagingWhatsAppFlow, Service: "social-messaging"})
	registerType(restype.Descriptor{Type: TypeSocialMessagingWhatsAppMessageTemplate, Service: "social-messaging"})
	registerService(serviceEntry{
		name: "aws:social-messaging",
		fn:   scanSocialMessaging,
	})
}

const (
	socialMessagingListFlowsOp     = "social-messaging:ListWhatsAppFlows"
	socialMessagingListTemplatesOp = "social-messaging:ListWhatsAppMessageTemplates"
	// socialMessagingPageLimit is the model's MaxResults cap for both child list ops.
	socialMessagingPageLimit = 100
)

// socialMessagingRetryableCodes are the child list ops' errors the model marks
// retryable. The SDK retries DependencyException (502) and
// InternalServiceException (500) by status, and ThrottledRequestException
// (429) through socialMessagingClientOptions. isTransientNetworkError matches
// none of them, so without a local skip one WABA's error that outlasts the
// retries would fail the phase and drop its siblings' rows.
var socialMessagingRetryableCodes = []string{"ThrottledRequestException", "DependencyException", "InternalServiceException"}

// socialMessagingPhoneNumberAttrs embeds the SDK phone-number summary plus the
// parent WABA ARN, which the list/get response doesn't carry per phone-number
// entry. The resolver reads WabaArn to wire the attached-to edge.
type socialMessagingPhoneNumberAttrs struct {
	socialmessagingtypes.WhatsAppPhoneNumberSummary
	WabaArn string `json:"WabaArn,omitempty"`
}

type socialMessagingAPI interface {
	ListLinkedWhatsAppBusinessAccounts(context.Context, *socialmessaging.ListLinkedWhatsAppBusinessAccountsInput, ...func(*socialmessaging.Options)) (*socialmessaging.ListLinkedWhatsAppBusinessAccountsOutput, error)
	GetLinkedWhatsAppBusinessAccount(context.Context, *socialmessaging.GetLinkedWhatsAppBusinessAccountInput, ...func(*socialmessaging.Options)) (*socialmessaging.GetLinkedWhatsAppBusinessAccountOutput, error)
	ListWhatsAppFlows(context.Context, *socialmessaging.ListWhatsAppFlowsInput, ...func(*socialmessaging.Options)) (*socialmessaging.ListWhatsAppFlowsOutput, error)
	ListWhatsAppMessageTemplates(context.Context, *socialmessaging.ListWhatsAppMessageTemplatesInput, ...func(*socialmessaging.Options)) (*socialmessaging.ListWhatsAppMessageTemplatesOutput, error)
}

// socialMessagingWABAs are the WABAs stored this scan: parents keys each by
// its "waba-…" id (what the child list ops take) and its ARN (the row's
// NativeID); incomplete holds the ids whose RegistrationStatus is not COMPLETE.
type socialMessagingWABAs struct {
	parents    []childParent
	incomplete map[string]bool
}

// scanSocialMessaging discovers linked WhatsApp Business Accounts (WABAs) and,
// per WABA, its registered phone numbers, WhatsApp Flows and message templates.
func scanSocialMessaging(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := socialmessaging.NewFromConfig(acct.cfg, socialMessagingClientOptions(region))
	return scanSocialMessagingAll(ctx, client, acct, region, st, scanID)
}

// socialMessagingClientOptions pins the client to region and makes it retry
// ThrottledRequestException, the service's HTTP 429 throttle: the SDK neither
// lists that code among its throttle codes nor retries a 429 by status, so a
// single throttle would otherwise fail the call unretried.
func socialMessagingClientOptions(region string) func(*socialmessaging.Options) {
	return func(o *socialmessaging.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, "ThrottledRequestException")
	}
}

// scanSocialMessagingAll runs the older phases, which stop at their first hard
// error, then the flow and message-template phases over the WABAs stored
// before that error, so a failure on either side costs the other no rows. The
// older side's transient error is demoted to a warning: one transient member
// would make the dispatcher read the whole join as transient, reporting a new
// phase's hard error as a transient one.
func scanSocialMessagingAll(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, wabas, err := scanSocialMessagingOlderPhases(ctx, client, acct, region, st, scanID)
	ft, fi, ferr := scanSocialMessagingFlows(ctx, client, acct, region, st, scanID, wabas)
	tt, ti, terr := scanSocialMessagingTemplates(ctx, client, acct, region, st, scanID, wabas)
	return total + ft + tt, inserted + fi + ti, errors.Join(demoteTransient(st, "social-messaging", acct, region, err), ferr, terr)
}

// scanSocialMessagingOlderPhases runs the phases that predate flows and
// message templates (WABAs, then each WABA's phone numbers), stopping at the
// first hard error, and returns the WABAs stored before it.
func scanSocialMessagingOlderPhases(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, wabas socialMessagingWABAs, err error) {
	wabas, total, inserted, err = scanSocialMessagingWABAs(ctx, client, acct, region, st, scanID)
	if err != nil {
		return 0, 0, socialMessagingWABAs{}, err
	}
	for _, p := range wabas.parents {
		t, i, perr := scanSocialMessagingPhoneNumbers(ctx, client, acct, region, st, scanID, p.id)
		if perr != nil {
			return total, inserted, wabas, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, wabas, nil
}

func scanSocialMessagingWABAs(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID string) (socialMessagingWABAs, int, int, error) {
	pager := socialmessaging.NewListLinkedWhatsAppBusinessAccountsPaginator(client, &socialmessaging.ListLinkedWhatsAppBusinessAccountsInput{})
	var batch []*store.Resource
	wabas := socialMessagingWABAs{incomplete: map[string]bool{}}
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return socialMessagingWABAs{}, 0, 0, skipIfAccessDenied(st, "social-messaging:ListLinkedWhatsAppBusinessAccounts", acct.ID, region, err)
			}
			return socialMessagingWABAs{}, 0, 0, fmt.Errorf("social-messaging:ListLinkedWhatsAppBusinessAccounts: %w", err)
		}
		for _, a := range out.LinkedAccounts {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			if id := sv(a.Id); id != "" {
				wabas.parents = append(wabas.parents, childParent{id: id, arn: arn})
				if a.RegistrationStatus != socialmessagingtypes.RegistrationStatusComplete {
					wabas.incomplete[id] = true
				}
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
	if err != nil {
		return socialMessagingWABAs{}, 0, 0, err
	}
	return wabas, t, i, nil
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

// scanSocialMessagingFlows stores each WABA's WhatsApp Flows under it. The API
// returns no flow ARN, so the NativeID is {wabaARN}/flow/{FlowId}; the model
// pins FlowId to digits.
func scanSocialMessagingFlows(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID string, wabas socialMessagingWABAs) (int, int, error) {
	return socialMessagingChildFanOut(ctx, st, acct, region, socialMessagingListFlowsOp, wabas, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
		pager := socialmessaging.NewListWhatsAppFlowsPaginator(client, &socialmessaging.ListWhatsAppFlowsInput{Id: &p.id},
			func(o *socialmessaging.ListWhatsAppFlowsPaginatorOptions) {
				o.Limit = socialMessagingPageLimit
				o.StopOnDuplicateToken = true
			})
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, f := range out.Flows {
				flowID := sv(f.FlowId)
				if flowID == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSocialMessagingWhatsAppFlow, NativeID: p.arn + "/flow/" + flowID,
					Name: f.FlowName, Region: &region, Status: nonEmptyPtr(sv(f.FlowStatus)),
					AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

// scanSocialMessagingTemplates stores each WABA's message templates under it.
// The API returns no template ARN, so the NativeID is
// {wabaARN}/message-template/{MetaTemplateId}: Meta gives every
// (name, language) variant its own id, and the model pins it to digits.
// TemplateName is not unique within a WABA (one per language) and is not used.
//
// The model leaves MetaTemplateId optional; a template without one is not
// stored, and the count of those is reported in one warning.
func scanSocialMessagingTemplates(ctx context.Context, client socialMessagingAPI, acct *account, region string, st *store.Store, scanID string, wabas socialMessagingWABAs) (int, int, error) {
	var missingID atomic.Int64
	total, inserted, err := socialMessagingChildFanOut(ctx, st, acct, region, socialMessagingListTemplatesOp, wabas, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
		pager := socialmessaging.NewListWhatsAppMessageTemplatesPaginator(client, &socialmessaging.ListWhatsAppMessageTemplatesInput{Id: &p.id},
			func(o *socialmessaging.ListWhatsAppMessageTemplatesPaginatorOptions) {
				o.Limit = socialMessagingPageLimit
				o.StopOnDuplicateToken = true
			})
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, tmpl := range out.Templates {
				templateID := sv(tmpl.MetaTemplateId)
				if templateID == "" {
					missingID.Add(1)
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSocialMessagingWhatsAppMessageTemplate, NativeID: p.arn + "/message-template/" + templateID,
					Name: tmpl.TemplateName, Region: &region, Status: nonEmptyPtr(sv(tmpl.TemplateStatus)),
					AttributesJSON: mustJSON(tmpl), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
	if n := missingID.Load(); n > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: socialMessagingListTemplatesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d template(s) without MetaTemplateId not stored", n),
		})
	}
	return total, inserted, err
}

// socialMessagingChildFanOut runs list for each WABA and stores the rows under
// it. A WABA unlinked since it was listed (ResourceNotFoundException) is
// skipped silently. An error the model marks retryable that still fails after
// the SDK's retries skips the WABA, with one warning per op and code. Two
// refusals skip the WABA with one warning per op,
// because neither has been observed and failing would fail every scan:
// AccessDeniedByMetaException, Meta's refusal for one WABA, which is not an
// IAM denial (isAccessDenied does not match it); and InvalidParametersException
// from a WABA whose registration is not complete. That code from a complete
// WABA is returned: the only other inputs are the id the service listed and a
// fixed page size.
func socialMessagingChildFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, wabas socialMessagingWABAs,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	const what = "WhatsApp Business Account(s)"
	var metaDenied, notReady skipTally
	retryable := make([]skipTally, len(socialMessagingRetryableCodes))
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, op, wabas.parents, isGone, byParentID,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := list(ctx, p)
			for i, code := range socialMessagingRetryableCodes {
				if retryable[i].dropCode(code, p.id, err) {
					return nil, nil
				}
			}
			if metaDenied.dropCode("AccessDeniedByMetaException", p.id, err) {
				return nil, nil
			}
			if wabas.incomplete[p.id] && notReady.dropCode("InvalidParametersException", p.id, err) {
				return nil, nil
			}
			return rows, err
		})
	for i, code := range socialMessagingRetryableCodes {
		retryable[i].warnCode(st, op, acct, region, code, what, "the model marks it retryable and it still failed after the SDK's retries")
	}
	metaDenied.warnCode(st, op, acct, region, "AccessDeniedByMetaException", what, "Meta refused access to them")
	notReady.warnCode(st, op, acct, region, "InvalidParametersException", what, "their registration is not complete")
	return total, inserted, err
}
