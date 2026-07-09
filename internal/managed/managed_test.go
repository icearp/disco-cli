package managed

import "testing"

func TestRegisterAndIs(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	if Is("aws:foo:bar") {
		t.Fatal("unregistered type reported managed")
	}
	Register("aws:foo:bar")
	if !Is("aws:foo:bar") {
		t.Fatal("registered type not reported managed")
	}
	// Idempotent + unrelated type stays unmanaged.
	Register("aws:foo:bar")
	if Is("aws:foo:baz") {
		t.Fatal("unrelated type reported managed")
	}
}

func TestRegisterEmptyIsNoOp(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	Register("")
	if Is("") {
		t.Fatal("empty type registered")
	}
}
