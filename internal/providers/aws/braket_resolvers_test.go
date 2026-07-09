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
	if !serviceRegistered("aws:braket") {
		t.Fatalf("aws:braket service not registered")
	}
	if !descriptorEmitted(TypeBraketSpendingLimit) {
		t.Fatalf("aws:braket emits missing %s", TypeBraketSpendingLimit)
	}
}
