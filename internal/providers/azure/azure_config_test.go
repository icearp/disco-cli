package azure

import "testing"

// TestResolveSubscriptionScope_PinSkipsEnumeration verifies an explicit pin
// uses exactly the supplied IDs and never reaches the ARM enumerator.
func TestResolveSubscriptionScope_PinSkipsEnumeration(t *testing.T) {
	enumerate := func() ([]subscription, error) {
		t.Fatal("enumerate called despite an explicit subscription pin (fail-open)")
		return nil, nil
	}
	subs, err := resolveSubscriptionScope([]string{"sub-a", "sub-b"}, providerCfg{}, enumerate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 2 || subs[0].ID != "sub-a" || subs[1].ID != "sub-b" {
		t.Fatalf("got %+v, want pinned [sub-a sub-b]", subs)
	}
}

// TestResolveSubscriptionScope_PinTrimsBlanks verifies surrounding whitespace
// and empty entries are dropped but a non-empty pin still resolves.
func TestResolveSubscriptionScope_PinTrimsBlanks(t *testing.T) {
	subs, err := resolveSubscriptionScope([]string{" sub-a ", "", "  "}, providerCfg{}, func() ([]subscription, error) {
		t.Fatal("enumerate must not run when a pin is set")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 1 || subs[0].ID != "sub-a" {
		t.Fatalf("got %+v, want [sub-a]", subs)
	}
}

// TestResolveSubscriptionScope_PinEmptyIsError verifies fail-closed: a pin that
// resolves to zero subscriptions errors instead of auto-enumerating, even when
// the config file lists subscriptions.
func TestResolveSubscriptionScope_PinEmptyIsError(t *testing.T) {
	_, err := resolveSubscriptionScope([]string{"", "  "}, providerCfg{Subscriptions: []subscriptionCfg{{ID: "cfg"}}}, func() ([]subscription, error) {
		t.Fatal("enumerate must not run when a pin is set")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected fail-closed error when pin resolves to zero subscriptions")
	}
}

// TestResolveSubscriptionScope_WhitespaceOnlyPinFailsClosed covers the
// subtlest Lighthouse boundary case: a whitespace-only pin with empty config
// must error, never fall through to enumerate (which would read every
// tenant's subscriptions under a delegated identity).
func TestResolveSubscriptionScope_WhitespaceOnlyPinFailsClosed(t *testing.T) {
	_, err := resolveSubscriptionScope([]string{"   "}, providerCfg{}, func() ([]subscription, error) {
		t.Fatal("enumerate must not run for a whitespace-only pin (fail-open)")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected fail-closed error for a whitespace-only pin")
	}
}

// TestResolveSubscriptionScope_NilUsesConfig verifies a nil override falls back
// to the config 'subscriptions:' list and does not enumerate.
func TestResolveSubscriptionScope_NilUsesConfig(t *testing.T) {
	subs, err := resolveSubscriptionScope(nil, providerCfg{Subscriptions: []subscriptionCfg{{ID: "cfg-sub", Name: "Prod"}}}, func() ([]subscription, error) {
		t.Fatal("enumerate must not run when config lists subscriptions")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 1 || subs[0].ID != "cfg-sub" || subs[0].Name != "Prod" {
		t.Fatalf("got %+v, want config sub", subs)
	}
}

// TestResolveSubscriptionScope_ConfigTrimsTheStoredID pins that the config
// branch stores the TRIMMED id, not the raw one. A padded id clears the
// fail-closed guard (which trims before testing for empty) and then fails the
// strings.EqualFold match in subscriptionResourceBatch, so the scan reports
// the pin as absent from the /subscriptions page and warns that the
// delegation may have been revoked — a confident wrong diagnosis for
// whitespace in a config file.
func TestResolveSubscriptionScope_ConfigTrimsTheStoredID(t *testing.T) {
	subs, err := resolveSubscriptionScope(nil, providerCfg{Subscriptions: []subscriptionCfg{{ID: "  cfg-sub\t", Name: " Prod\n"}}}, func() ([]subscription, error) {
		t.Fatal("enumerate must not run when config lists subscriptions")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 1 || subs[0].ID != "cfg-sub" {
		t.Fatalf("got %+v, want the id trimmed to %q", subs, "cfg-sub")
	}
	// Name is display-only — it reaches Resource.AccountName and the scope
	// label — so a padded one is cosmetic rather than a wrong diagnosis, but
	// it is the same asymmetry and the same one-line fix.
	if subs[0].Name != "Prod" {
		t.Errorf("Name = %q; want it trimmed to %q", subs[0].Name, "Prod")
	}
}

// TestResolveSubscriptionScope_NilEmptyConfigEnumerates verifies the default
// path: nil override + empty config triggers auto-enumeration.
func TestResolveSubscriptionScope_NilEmptyConfigEnumerates(t *testing.T) {
	called := false
	subs, err := resolveSubscriptionScope(nil, providerCfg{}, func() ([]subscription, error) {
		called = true
		return []subscription{{ID: "enum-sub"}}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("enumerate should run when override nil and config empty")
	}
	if len(subs) != 1 || subs[0].ID != "enum-sub" {
		t.Fatalf("got %+v, want enumerated", subs)
	}
}
