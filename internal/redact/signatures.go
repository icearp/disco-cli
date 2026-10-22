package redact

import "sync"

// Signatures adds value shapes to the RedactUnlessSafe classifier. A provider
// package registers its own from init(); the classifier applies the core
// shapes plus every registered one to every value, whatever the row's
// provider, because a credential or reference of one cloud can sit in another
// cloud's resource. A shape is known only to builds that link the package
// registering it: a slim build without aws does not recognise an AWS access
// key as a credential. It is still redacted there only because the value is
// not on any safe list, so a provider that registers a Reference or
// ResourceID shape must not match strings that can carry another cloud's
// credential.
type Signatures struct {
	// Credential matches a string containing a credential anywhere in it. A
	// match redacts the value under any key, even inside a reference shape.
	Credential []func(string) bool
	// Reference matches a whole string that names where a secret lives
	// without holding it. A match is kept under any key.
	Reference []func(string) bool
	// ResourceID matches a whole string that is a resource identifier. A
	// match is kept only under a key name that is not secret-looking.
	ResourceID []func(string) bool
}

var (
	sigMu sync.RWMutex
	// registered is read on every classify call rather than snapshotted at
	// package init, because provider init() functions run after this
	// package's.
	registered Signatures
)

// RegisterSignatures adds s to the shapes every classified value is checked
// against.
func RegisterSignatures(s Signatures) {
	sigMu.Lock()
	defer sigMu.Unlock()
	registered.Credential = append(registered.Credential, s.Credential...)
	registered.Reference = append(registered.Reference, s.Reference...)
	registered.ResourceID = append(registered.ResourceID, s.ResourceID...)
}

// registeredSignatures returns the provider-registered shapes. Registration
// only appends, so the returned slice headers stay valid after the lock is
// released.
func registeredSignatures() Signatures {
	sigMu.RLock()
	defer sigMu.RUnlock()
	return registered
}

func anyMatch(matchers []func(string) bool, s string) bool {
	for _, m := range matchers {
		if m(s) {
			return true
		}
	}
	return false
}
