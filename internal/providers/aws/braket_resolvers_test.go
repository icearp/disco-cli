package aws

import "testing"

func TestResolveBraketSpendingLimits(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveBraketSpendingLimits(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestBraketRegistrationCovered(t *testing.T) {
	t.Parallel()
	if !serviceRegistered("aws:braket") {
		t.Fatalf("aws:braket service not registered")
	}
	if !descriptorEmitted(TypeBraketSpendingLimit) {
		t.Fatalf("aws:braket emits missing %s", TypeBraketSpendingLimit)
	}
}
