package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mturk"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// AnswerKey holds the correct answers to the qualification test: anyone
	// who reads it can pass the test and be granted the qualification, so it
	// is treated as a credential. The Test question form itself is shown to
	// every worker who takes the test and is kept.
	registerType(restype.Descriptor{Type: TypeMTurkQualificationType, Service: "mturk-requester", Redact: []redact.Rule{
		{Path: "AnswerKey", Mode: redact.RedactScalar},
	}})
	registerService(serviceEntry{
		name:   "aws:mturk",
		global: true,
		optIn:  true,
		fn:     scanMTurk,
	})
}

// mturkPageLimit is the model's MaxResults cap (ResultSize range 1-100).
const mturkPageLimit = 100

type mturkAPI interface {
	ListQualificationTypes(context.Context, *mturk.ListQualificationTypesInput, ...func(*mturk.Options)) (*mturk.ListQualificationTypesOutput, error)
}

// scanMTurk lists the Mechanical Turk qualification types the account created.
// The production requester endpoint exists only in us-east-1 (the SDK endpoint
// table lists us-east-1 and the "sandbox" pseudo-region), so the service is
// global and pinned there; the sandbox is never scanned.
//
// The service is opt-in (--services aws:mturk): the API answers only an AWS
// account whose root user has linked it to an MTurk requester account, and
// the MTurk Getting Started guide says MTurk does not currently support IAM
// role credentials. A default scan run on role credentials, or against an
// unlinked account, would therefore record an error for this service on most
// accounts.
func scanMTurk(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region := "us-east-1"
	client := mturk.NewFromConfig(acct.cfg, func(o *mturk.Options) { o.Region = region })
	return scanMTurkQualificationTypes(ctx, client, acct, region, st, scanID)
}

// mturkQualificationTypeNativeID synthesizes an ARN-shaped id: MTurk issues no
// ARN for a qualification type and the Service Reference lists no resource
// for it. The namespace is the model's arnNamespace.
func mturkQualificationTypeNativeID(acctID, region, id string) string {
	return fmt.Sprintf("arn:aws:mturk-requester:%s:%s:qualification-type/%s", region, acctID, id)
}

// scanMTurkQualificationTypes lists only the caller's own types: without
// MustBeOwnedByCaller the operation searches the whole public marketplace
// catalogue. MustBeRequestable is required; false means "do not filter on
// requestability" (model doc: "If false, all Qualification types, including
// those managed by the system, are considered"), so one listing returns owned
// requestable and non-requestable types alike. The list element is the same
// QualificationType shape GetQualificationType returns, so no Get fan-out. The
// loop is hand-rolled because the SDK paginator either follows a repeated
// token forever or, with StopOnDuplicateToken, stops without an error.
func scanMTurkQualificationTypes(ctx context.Context, client mturkAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	noID := 0
	defer func() {
		if noID > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: "mturk-requester:ListQualificationTypes", Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d qualification type(s) not stored: the response gave no QualificationTypeId", noID),
			})
		}
	}()
	var nextToken *string
	for {
		out, err := client.ListQualificationTypes(ctx, &mturk.ListQualificationTypesInput{
			MustBeOwnedByCaller: sdkaws.Bool(true),
			MustBeRequestable:   sdkaws.Bool(false),
			MaxResults:          sdkaws.Int32(mturkPageLimit),
			NextToken:           nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "mturk-requester:ListQualificationTypes", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("mturk-requester:ListQualificationTypes: %w", err)
		}
		batch := make([]*store.Resource, 0, len(out.QualificationTypes))
		for _, q := range out.QualificationTypes {
			id := sv(q.QualificationTypeId)
			if id == "" {
				noID++
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMTurkQualificationType,
				NativeID:       mturkQualificationTypeNativeID(acct.ID, region, id),
				Name:           q.Name,
				Status:         nonEmptyPtr(string(q.QualificationTypeStatus)),
				Region:         &region,
				CreatedAt:      tp(q.CreationTime),
				AttributesJSON: mustJSON(q),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert mturk qualification types: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
		next := sv(out.NextToken)
		if next == "" {
			return total, inserted, nil
		}
		if next == sv(nextToken) {
			return total, inserted, fmt.Errorf("mturk-requester:ListQualificationTypes: NextToken %q: %w", next, errRepeatedNextToken)
		}
		nextToken = out.NextToken
	}
}
