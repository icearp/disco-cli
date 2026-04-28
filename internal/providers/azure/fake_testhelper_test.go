package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// fakeCred returns an azcore.TokenCredential that the SDK auth pipeline accepts
// without ever issuing a real auth call. Paired with a fake transport, no HTTP
// or token exchange occurs.
func fakeCred() azcore.TokenCredential { return &fake.TokenCredential{} }

// fakeClientOptions returns *arm.ClientOptions wired to short-circuit every
// request through the supplied fake server transport. Tests use this in place
// of azClientOptions when constructing arm* clients so that NewListPager and
// friends never touch the network.
//
// The retry policy is collapsed (MaxRetries=0) because a fake transport
// returning a deterministic response should never trigger retries; if it does,
// the test wants the error surfaced immediately rather than masked by the
// production retry loop.
func fakeClientOptions(t *testing.T, transport policy.Transporter) *arm.ClientOptions {
	t.Helper()
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: 0},
		},
	}
}
