package untaggable

import (
	"slices"
	"testing"
)

func TestRegister_IsAndTypes(t *testing.T) {
	Register("test:untag:b")
	Register("test:untag:a")
	Register("test:untag:a")
	Register("")

	if !Is("test:untag:a") || !Is("test:untag:b") {
		t.Fatal("registered type not reported untaggable")
	}
	if Is("test:untag:c") {
		t.Fatal("unregistered type reported untaggable")
	}
	if Is("") {
		t.Fatal("empty type registered")
	}
	got := Types()
	if !slices.IsSorted(got) {
		t.Errorf("Types() not sorted: %v", got)
	}
	for _, want := range []string{"test:untag:a", "test:untag:b"} {
		if !slices.Contains(got, want) {
			t.Errorf("Types() = %v; missing %s", got, want)
		}
	}
	if slices.Contains(got, "") {
		t.Errorf("Types() = %v; lists the empty type", got)
	}
}
