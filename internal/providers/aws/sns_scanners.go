package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSNSTopic, Service: "sns"})
	registerType(restype.Descriptor{Type: TypeSNSSubscription, Service: "sns"})
	registerType(restype.Descriptor{Type: TypeSNSTopicPolicy, Service: "sns"})
	// The SDK documents PlatformCredential (client secret, private key, signing
	// key, API key, secret key or a JSON private key file) and PlatformPrincipal
	// (client id, certificate, signing key id, or Baidu's API key) as attributes
	// set on create/update; the GetPlatformApplicationAttributes doc lists neither
	// among the attributes it returns.
	// Whether ListPlatformApplications ever returns them is not verified, so both
	// are redacted in case it does. The other attributes (event topic ARNs,
	// feedback role ARNs, Enabled, certificate expiry, Apple team/bundle ids)
	// identify no secret and are kept.
	registerType(restype.Descriptor{Type: TypeSNSPlatformApplication, Service: "sns", Redact: []redact.Rule{
		{Path: "Attributes.PlatformCredential", Mode: redact.RedactScalar},
		{Path: "Attributes.PlatformPrincipal", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeSNSOriginationNumber, Service: "sns"})
	registerService(serviceEntry{
		name: "aws:sns",
		fn:   scanSNS,
	})
}

const (
	snsListPlatformApplicationsOp        = "sns:ListPlatformApplications"
	snsListOriginationNumbersOp          = "sns:ListOriginationNumbers"
	snsOriginationNumbersPageLimit int32 = 30 // model cap (MaxItemsListOriginationNumbers 1..30)
)

// snsAPI is the narrow set of SNS operations scanSNS calls. *sns.Client
// satisfies it; tests supply a hand-rolled stub. Methods keep the SDK's
// variadic option-fn shape so `NewListTopicsPaginator(client, ...)` still
// compiles.
type snsAPI interface {
	ListTopics(context.Context, *sns.ListTopicsInput, ...func(*sns.Options)) (*sns.ListTopicsOutput, error)
	GetTopicAttributes(context.Context, *sns.GetTopicAttributesInput, ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error)
	ListSubscriptions(context.Context, *sns.ListSubscriptionsInput, ...func(*sns.Options)) (*sns.ListSubscriptionsOutput, error)
	ListPlatformApplications(context.Context, *sns.ListPlatformApplicationsInput, ...func(*sns.Options)) (*sns.ListPlatformApplicationsOutput, error)
	ListOriginationNumbers(context.Context, *sns.ListOriginationNumbersInput, ...func(*sns.Options)) (*sns.ListOriginationNumbersOutput, error)
}

// scanSNS discovers SNS topics, subscriptions and topic policies, then
// platform applications and origination numbers. The two later phases run
// whatever the topic phases returned, so a failure in one costs the others
// none of their rows. Each phase's transient error is demoted to a warning,
// since one transient member would make the dispatcher read the whole join as
// transient; the rest are joined and the counts summed.
func scanSNS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := sns.NewFromConfig(acct.cfg, func(o *sns.Options) { o.Region = region })
	total, inserted, err := scanSNSTopicPhases(ctx, client, acct, region, st, scanID)
	errs := []error{demoteTransient(st, "sns", acct, region, err)}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{snsListPlatformApplicationsOp, func() (int, int, error) {
			return scanSNSPlatformApplications(ctx, client, acct, region, st, scanID)
		}},
		{snsListOriginationNumbersOp, func() (int, int, error) {
			return scanSNSOriginationNumbers(ctx, client, acct, region, st, scanID)
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	return total, inserted, errors.Join(errs...)
}

// scanSNSTopicPhases runs the topic phase, then the subscription and topic
// policy phases, stopping at the first error. ListTopics returns only ARNs;
// GetTopicAttributes runs concurrently to fetch the attributes map
// (KmsMasterKeyId, RedrivePolicy, etc.) the resolver needs.
func scanSNSTopicPhases(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanSNSTopics(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanSNSExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanSNSTopics is the testable scan body: depends only on the narrow snsAPI
// interface so unit tests can inject a stub client without HTTP mocks.
// scanSNS wires the concrete *sns.Client.
//
// A denied GetTopicAttributes drops that topic and warns once for the phase.
// pageScanConcurrent warns only for the shared denial codes, so SNS's own
// AuthorizationError from ListTopics is mapped to a warning here.
func scanSNSTopics(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const listOp = "sns:ListTopics"
	p := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	var denyOnce sync.Once
	total, inserted, err := pageScanConcurrent(ctx, listOp, acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*sns.ListTopicsOutput, error) { return p.NextPage(c) },
		func(o *sns.ListTopicsOutput) []string {
			out := make([]string, 0, len(o.Topics))
			for _, t := range o.Topics {
				out = append(out, sv(t.TopicArn))
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			attrsOut, err := client.GetTopicAttributes(gctx, &sns.GetTopicAttributesInput{TopicArn: &arn})
			if err != nil {
				if isSNSDenied(err) {
					denyOnce.Do(func() { _ = skipIfAccessDenied(st, "sns:GetTopicAttributes", acct.ID, region, err) })
					return nil, nil
				}
				return nil, fmt.Errorf("sns:GetTopicAttributes %s: %w", arn, err)
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSNSTopic,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(attrsOut.Attributes),
				DiscoveredBy:   scanID,
			}, nil
		}, 0)
	if isSNSDenied(err) {
		return total, inserted, skipIfAccessDenied(st, listOp, acct.ID, region, err)
	}
	return total, inserted, err
}

// isSNSDenied reports an IAM denial. SNS answers one with its own modeled
// code, AuthorizationError, which accessDeniedCodes does not list.
func isSNSDenied(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, "AuthorizationError")
}

// snsListPages stores each page's rows as it is read, so a failure on a later
// page keeps the earlier pages. A denial warns and ends the phase. On the first
// page only, an operation the region's endpoint does not serve ends the phase
// silently: AWS documents mobile push and SMS for a subset of SNS regions.
// What the other regions answer has not been observed, so only the shared
// not-deployed shapes are skipped. Every other error is returned wrapped with
// op.
func snsListPages[Page any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	hasMore func() bool, next func(context.Context) (Page, error), rows func(Page) []*store.Resource,
) (total, inserted int, err error) {
	for first := true; hasMore(); first = false {
		page, err := next(ctx)
		if err != nil {
			switch {
			case isSNSDenied(err):
				return total, inserted, skipIfAccessDenied(st, op, acct.ID, region, err)
			case first && isOpNotDeployed(err):
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("%s: %w", op, err)
		}
		t, i, err := upsertBatch(st, rows(page), op)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// snsPlatformApplicationName is the application name, the last segment of
// its ARN (arn:aws:sns:{region}:{account}:app/{platform}/{name}).
func snsPlatformApplicationName(arn string) string {
	return arn[strings.LastIndexByte(arn, '/')+1:]
}

// scanSNSPlatformApplications stores the mobile push platform applications.
// The listing carries each application's attributes; the descriptor's redact
// rules keep any push credential out of the store.
func scanSNSPlatformApplications(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := sns.NewListPlatformApplicationsPaginator(client, &sns.ListPlatformApplicationsInput{},
		func(o *sns.ListPlatformApplicationsPaginatorOptions) { o.StopOnDuplicateToken = true })
	return snsListPages(ctx, st, acct, region, snsListPlatformApplicationsOp, p.HasMorePages,
		func(c context.Context) (*sns.ListPlatformApplicationsOutput, error) { return p.NextPage(c) },
		func(out *sns.ListPlatformApplicationsOutput) []*store.Resource {
			var batch []*store.Resource
			for _, app := range out.PlatformApplications {
				arn := sv(app.PlatformApplicationArn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSNSPlatformApplication, NativeID: arn,
					Name: nonEmptyPtr(snsPlatformApplicationName(arn)), Region: &region,
					AttributesJSON: mustJSON(app), DiscoveredBy: scanID,
				})
			}
			return batch
		})
}

// snsOriginationNumberNativeID keys an origination number: the API returns no
// ARN and the Service Reference defines none. The phone number is E.164
// (model pattern ^(\+[0-9]{8,}|[0-9]{0,9})$, no "/"), and a topic name cannot
// contain "/", so the id cannot collide with a topic ARN. The model marks
// PhoneNumber @sensitive, yet it is the id and Name: redaction is for
// credentials, and this is the account's own sending number (as for
// smsvoice phone numbers and location's).
func snsOriginationNumberNativeID(region, accountID, phoneNumber string) string {
	return "arn:aws:sns:" + region + ":" + accountID + ":origination-number/" + phoneNumber
}

// scanSNSOriginationNumbers stores the account's dedicated SMS origination
// numbers.
func scanSNSOriginationNumbers(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := sns.NewListOriginationNumbersPaginator(client, &sns.ListOriginationNumbersInput{},
		func(o *sns.ListOriginationNumbersPaginatorOptions) {
			o.Limit = snsOriginationNumbersPageLimit
			o.StopOnDuplicateToken = true
		})
	return snsListPages(ctx, st, acct, region, snsListOriginationNumbersOp, p.HasMorePages,
		func(c context.Context) (*sns.ListOriginationNumbersOutput, error) { return p.NextPage(c) },
		func(out *sns.ListOriginationNumbersOutput) []*store.Resource {
			var batch []*store.Resource
			for _, n := range out.PhoneNumbers {
				phone := sv(n.PhoneNumber)
				if phone == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSNSOriginationNumber, NativeID: snsOriginationNumberNativeID(region, acct.ID, phone),
					Name: &phone, Region: &region, Status: nonEmptyPtr(sv(n.Status)),
					CreatedAt: tp(n.CreatedAt), AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
				})
			}
			return batch
		})
}
