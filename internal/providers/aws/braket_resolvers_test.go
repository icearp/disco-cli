package aws

import "testing"

func TestResolveBraketSpendingLimits(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveBraketSpendingLimits(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestBraketRegistrationCovered(t *testing.T) {
	for _, s := range registeredServices {
		if s.name != "aws:braket" {
			continue
		}
		for _, e := range s.emits {
			if e.DiscoType == TypeBraketSpendingLimit {
				return
			}
		}
		t.Fatalf("aws:braket emits missing %s", TypeBraketSpendingLimit)
	}
	t.Fatalf("aws:braket service not registered")
}
