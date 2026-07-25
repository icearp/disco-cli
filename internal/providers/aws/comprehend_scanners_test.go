package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	"github.com/icearp/disco-cli/store"
)

// stubComprehend returns a canned error from every List op. Each scanner phase
// calls exactly one of them, so a single err field covers all four.
type stubComprehend struct{ err error }

func (s stubComprehend) ListDocumentClassifiers(context.Context, *comprehend.ListDocumentClassifiersInput, ...func(*comprehend.Options)) (*comprehend.ListDocumentClassifiersOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListEntityRecognizers(context.Context, *comprehend.ListEntityRecognizersInput, ...func(*comprehend.Options)) (*comprehend.ListEntityRecognizersOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListEndpoints(context.Context, *comprehend.ListEndpointsInput, ...func(*comprehend.Options)) (*comprehend.ListEndpointsOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListFlywheels(context.Context, *comprehend.ListFlywheelsInput, ...func(*comprehend.Options)) (*comprehend.ListFlywheelsOutput, error) {
	return nil, s.err
}

func comprehendPhases() map[string]func(context.Context, comprehendAPI, *account, string, *store.Store, string) (int, int, error) {
	return map[string]func(context.Context, comprehendAPI, *account, string, *store.Store, string) (int, int, error){
		"document-classifiers": scanComprehendDocumentClassifiers,
		"entity-recognizers":   scanComprehendEntityRecognizers,
		"endpoints":            scanComprehendEndpoints,
		"flywheels":            scanComprehendFlywheels,
	}
}

// An account not subscribed to Comprehend's custom-model surface gets
// NotAuthorizedException "Your account is not authorized to make this call."
// That code is in accessDeniedCodes, so without the guard every phase recorded
// an IAM-style warning on every scan of every such region.
func TestScanComprehendPhases_SilentSkipWhenNotEnabled(t *testing.T) {
	notEnabled := apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")

	for name, phase := range comprehendPhases() {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := false
			st.OnWarn = func(store.ScanWarning) { warned = true }

			total, inserted, err := phase(
				context.Background(), stubComprehend{err: notEnabled}, newTestAccount(testAccountID), "eu-west-3", st, testScanID)
			if err != nil {
				t.Fatalf("not-enabled state must not surface an error, got %v", err)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("want (0,0), got (%d,%d)", total, inserted)
			}
			if warned {
				t.Error("account-not-subscribed must not record a scan warning")
			}
		})
	}
}

// The guard keys on the message, so a genuine per-action IAM denial sharing the
// NotAuthorizedException/AccessDenied code family still warns rather than being
// silently swallowed.
func TestScanComprehendPhases_RealDenialStillWarns(t *testing.T) {
	realDeny := apiErr("AccessDeniedException",
		"User: arn:aws:sts::123456789012:assumed-role/DiscoScanner/x is not authorized to perform: comprehend:ListEndpoints")

	for name, phase := range comprehendPhases() {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := false
			st.OnWarn = func(store.ScanWarning) { warned = true }

			if _, _, err := phase(
				context.Background(), stubComprehend{err: realDeny}, newTestAccount(testAccountID), "us-east-1", st, testScanID); err != nil {
				t.Fatalf("access-denied path returns nil, got %v", err)
			}
			if !warned {
				t.Error("a real IAM denial must still record a scan warning")
			}
		})
	}
}

func TestIsComprehendNotEnabled(t *testing.T) {
	if !isComprehendNotEnabled(apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")) {
		t.Error("not-subscribed message should match")
	}
	// Same code family, real per-action denial — must not match.
	if isComprehendNotEnabled(apiErr("NotAuthorizedException", "User: arn:... is not authorized to perform: comprehend:ListEndpoints")) {
		t.Error("real IAM denial must not match")
	}
	// Right message, wrong code.
	if isComprehendNotEnabled(apiErr("AccessDeniedException", "account is not authorized to make this call")) {
		t.Error("wrong code must not match")
	}
	if isComprehendNotEnabled(nil) {
		t.Error("nil must not match")
	}
}
