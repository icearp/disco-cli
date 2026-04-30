package aws

import "testing"

// resolveBillingViews is an audit-stub no-op (BillingViewListElement carries
// no cross-resource ARNs). Test pins the contract: zero edges emitted, no
// error. Future expansion wiring SourceViews edges should update this test.
func TestResolveBillingViews(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveBillingViews(acct, st); err != nil {
		t.Fatalf("resolveBillingViews: %v", err)
	}
}

func TestBillingViewRegistrationCovered(t *testing.T) {
	for _, s := range registeredServices {
		if s.name != "aws:billing" {
			continue
		}
		for _, e := range s.emits {
			if e.DiscoType == TypeBillingView {
				return
			}
		}
		t.Fatalf("aws:billing emits missing %s", TypeBillingView)
	}
	t.Fatalf("aws:billing service not registered")
}
