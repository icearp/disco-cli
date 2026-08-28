package azure

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Keyless Azure access from an AWS workload — the ECS/Fargate env contract.
//
// DefaultAzureCredential cannot authenticate in a distroless Fargate task:
// EnvironmentCredential needs a secret, certificate or password, workload
// identity needs the AKS
// webhook variables, managed identity needs Azure IMDS, and the CLI links need
// binaries the image does not carry. This path instead asks AWS STS for a
// signed JWT asserting the task's own AWS identity
// (sts:GetWebIdentityToken) and presents it to Microsoft Entra as a client
// assertion against a federated identity credential. No secret is stored on
// either side.
//
// Every value here is a non-secret public identifier: the Entra application
// (client) id, disco's own Entra tenant, the exchange audience, and an
// optional AssumeRole hop. The AWS subject the Entra trust names is the
// running role, retrieved at runtime.
//
// Mirrors the GCP contract in ../gcp/wif.go, which solves the same problem
// against Google's external-account exchange.

const (
	envWIFClientID    = "DISCO_AZURE_WIF_CLIENT_ID"
	envWIFTenantID    = "DISCO_AZURE_WIF_TENANT_ID"
	envWIFAudience    = "DISCO_AZURE_WIF_AUDIENCE"
	envWIFRoleARN     = "DISCO_AZURE_WIF_ROLE_ARN"
	envWIFSessionName = "DISCO_AZURE_WIF_SESSION_NAME"
	// envGraphTenantID names the DIRECTORY Microsoft Graph calls are aimed at,
	// which under Azure Lighthouse is the customer's and not the credential's
	// own. Set it only for a directory whose administrator has granted this
	// application the Graph application permissions; see
	// [wifConfig.graphTenantEnabled].
	envGraphTenantID = "DISCO_AZURE_GRAPH_TENANT_ID"
)

const (
	// tokenExchangeAudience is the audience Entra expects on a federated
	// identity credential in the public cloud, and the default here.
	// DISCO_AZURE_WIF_AUDIENCE exists to override it because a sovereign cloud
	// need not use this literal; it should otherwise be left alone.
	tokenExchangeAudience = "api://AzureADTokenExchange"
	// webIdentitySigningAlg is not a preference. Entra supports RS256 alone for
	// workload identity federation, while AWS's own examples use ES384 and the
	// parameter is required with no default — so copying an example, or reading
	// past it, produces a token that fails only at exchange time.
	webIdentitySigningAlg = "RS256"
	// webIdentityTokenTTL bounds the assertion's life. AWS permits 60s-3600s
	// and defaults to 300.
	//
	// NOT the 60s floor, though the assertion is spent immediately on one
	// exchange: Entra validates the JWT's own exp against ENTRA's clock, so
	// the usable window is the TTL minus the skew between two clouds' clocks
	// minus the exchange itself. At the floor a modest skew expires the
	// assertion before it is read, and that failure does not present as a
	// clock problem — retryCredential only knows AADSTS70021, so an expiry
	// surfaces as an unretried authentication failure.
	webIdentityTokenTTL = 300
)

// wifConfig is the resolved env contract. Zero value means "not federated",
// which leaves DefaultAzureCredential in charge — the standalone CLI must be
// unaffected by any of this.
type wifConfig struct {
	clientID    string
	tenantID    string
	audience    string
	roleARN     string
	sessionName string
	// graphTenantID is the customer directory Graph is aimed at. Separate from
	// tenantID, which is the directory the CREDENTIAL authenticates in; under
	// Lighthouse those differ and conflating them is the disclosure
	// [wifConfig.tenantScopeEnabled] exists to prevent.
	graphTenantID string
	// subscriptionTenantID is the directory every scanned subscription must
	// be owned by, checked against ARM in [bindSubscriptions]. Usually the
	// same value as graphTenantID, and deliberately a separate variable: that
	// one says which directory a Graph token is aimed at, this one says which
	// directory ARM must agree owns the subscriptions, and only the second is
	// checked against a second source.
	subscriptionTenantID string
}

// wifEnv reads the contract from the environment. Read once per scan and
// passed down, rather than consulted at each use, so one run cannot observe
// two different configurations.
func wifEnv() wifConfig {
	// Trimmed, because a value injected from a file or a secret store arrives
	// with a trailing newline often enough to matter. Untrimmed it still fails
	// CLOSED here — a padded client id satisfies configured(), so the guards
	// stay on — but it then fails at the exchange with an Entra code rather
	// than naming the variable, which is the diagnosis the eager checks exist
	// to give.
	return wifConfig{
		clientID:             strings.TrimSpace(os.Getenv(envWIFClientID)),
		tenantID:             strings.TrimSpace(os.Getenv(envWIFTenantID)),
		audience:             strings.TrimSpace(os.Getenv(envWIFAudience)),
		roleARN:              strings.TrimSpace(os.Getenv(envWIFRoleARN)),
		sessionName:          strings.TrimSpace(os.Getenv(envWIFSessionName)),
		graphTenantID:        strings.TrimSpace(os.Getenv(envGraphTenantID)),
		subscriptionTenantID: strings.TrimSpace(os.Getenv(envSubscriptionTenantID)),
	}
}

// configured reports whether both required halves are present. The audience
// has a correct default and the AssumeRole hop is optional, so neither counts.
func (c wifConfig) configured() bool { return c.clientID != "" && c.tenantID != "" }

// partiallyConfigured reports whether the deployment meant to federate but did
// not finish saying so.
//
// This is the same argument [wifSubjectCredentials] makes about a half-set
// AssumeRole hop, applied to the pair with the larger blast radius. Falling
// back to DefaultAzureCredential on a half-set identity would not merely pick a
// different credential: [wifConfig.tenantScopeEnabled] reads configured() too,
// so one typo'd variable name in a task definition silently re-enables the
// whole tenant phase and unpins subscription enumeration — the cross-customer
// disclosure both guards exist to prevent, in the one deployment where they are
// load-bearing. An operator's laptop sets none of these and is unaffected.
//
// [envGraphTenantID] counts, and it is the member that would be worst to
// leave out. Alone it is not merely inert: configured() is false, so
// tenantScopeEnabled() is TRUE, every tenant service runs, and scanEntra reads
// whatever directory the ambient credential authenticated in with no pin at
// all — the exact disclosure that variable is advertised to prevent, switched
// on by setting it. The realistic arrival is a deployment that still holds
// AZURE_CLIENT_ID/AZURE_CLIENT_SECRET for a Lighthouse managing principal
// while a rename or a rollback drops the WIF pair.
//
// [envSubscriptionTenantID] deliberately does NOT count, and it is the one
// DISCO_AZURE_ variable that does not. The test above is "would this value,
// alone, open something": that one opens nothing — it only adds a refusal, and
// [bindSubscriptions] runs it against an unfederated credential too, which is
// a supported standalone use. Counting it would turn the one variable that
// narrows a scan into a reason to refuse the scan outright.
func (c wifConfig) partiallyConfigured() bool {
	if c.configured() {
		return false
	}
	return c.clientID != "" || c.tenantID != "" || c.audience != "" ||
		c.graphTenantID != "" || c.sessionRequested()
}

// ErrIncompleteWIFConfig reports a half-declared federation. See
// [wifConfig.partiallyConfigured].
var ErrIncompleteWIFConfig = errors.New("azure wif: " + envWIFClientID + " and " + envWIFTenantID +
	" must both be set to federate; refusing to fall back to an ambient credential (fail-closed). " +
	envWIFAudience + ", " + envWIFRoleARN + ", " + envWIFSessionName + " or " + envGraphTenantID +
	" set WITHOUT both of those triggers this — " + envGraphTenantID +
	" grants nothing on its own and would leave every tenant-scope service reading the credential's own directory. " +
	envSubscriptionTenantID + " is the exception and never triggers it: it opens nothing and is usable unfederated")

// effectiveAudience is the configured audience, or the only value Entra
// accepts.
func (c wifConfig) effectiveAudience() string {
	if c.audience != "" {
		return c.audience
	}
	return tokenExchangeAudience
}

// sessionRequested reports whether either half of the AssumeRole hop was set,
// i.e. whether the caller meant to present a named session at all.
func (c wifConfig) sessionRequested() bool { return c.roleARN != "" || c.sessionName != "" }

// sessionComplete reports whether both halves were set.
func (c wifConfig) sessionComplete() bool { return c.roleARN != "" && c.sessionName != "" }

// tenantScopeEnabled reports whether tenant-scope Azure services may run.
//
// This is the cross-tenant guard, and it is deliberately broader than the
// Entra scanners. Under Azure Lighthouse the customer delegates SUBSCRIPTION
// scope only: authentication happens in the MANAGING tenant and Azure Resource
// Manager resolves the delegation, so every tenant-scope API this credential
// can reach answers about DISCO'S OWN directory, not the customer's. Every
// registered tenant service is affected by default (grep -n 'registerTenantService' *.go),
// as are tenantDisplayName and tenantIDFromCredScope, which Scan calls outside
// the service registry, and the management-group Entities list in
// stitchTopHierarchy, which is a different call from scanManagementTenant's
// flat list; all of them SUCCEED rather than fail.
//
// The registry half of that is now decided per service, by
// tenantServiceRunnable, which asks THIS function first and admits a Graph
// service against a named directory second — see
// [wifConfig.graphTenantEnabled]. A tenant service added later is still
// refused without being named here, because graphScoped is opt-in and the
// default is the suppression. Two callers still read this function DIRECTLY,
// and the grep above is how to re-derive them rather than trusting this list:
// the block in Scan that stamps subscription.tenantID and fetches the tenant
// display name, and stitchTopHierarchy's management-group Entities call. Both
// are correct to, for DIFFERENT reasons. The Entities call is ARM, which names
// no directory, so no token can redirect it. The block in Scan is NOT: it
// resolves the tid from an ARM token and then calls tenantDisplayName, which
// is Microsoft Graph and which a token could redirect — newGraphClient(cred,
// "") is what keeps it on disco's directory. Redirecting only that half would
// hang a customer's directory NAME on an ARM-derived id, so the two move
// together or not at all. (tenantDisplayName and tenantIDFromCredScope test
// nothing themselves — they sit INSIDE that one block, which is one call site
// and not two.)
//
// That grep is NOT the whole set, though, and reading it as one is its own
// mistake. A call is tenant-wide because of its URL, not because of which
// phase registered it: GET /subscriptions runs inside the per-subscription
// fan-out and is covered instead by enumerateScope and
// subscriptionResourceBatch. The one other instance we had found,
// armautomanage's BestPractices.ListByTenant, was left ungated on the grounds
// that its payload is Microsoft's own catalog — the wrong axis, because the
// row it stored asserted a successful read of a subscription that had refused
// every other call. That scanner is gone (the service retires in September
// 2027), so no live example remains and the set is UNENUMERATED, not empty.
// To re-derive it, look for ARM operations whose URL template carries no
// {subscriptionId} or {scope} segment and check each against these guards — a
// service-registry grep cannot see any of them.
//
// Left ungated, a Lighthouse-only scan writes disco's directory into the
// customer's inventory as their resources. That is a cross-customer
// disclosure, and because Graph and ARM both ANSWER, nothing in the scan looks
// wrong — the permission failures the Entra scanner reports never happen.
//
// There is deliberately NO env var that reopens ARM tenant scope, and this
// function is not the one [envGraphTenantID] affects. Microsoft GRAPH can be
// aimed at a named customer directory, because a token can carry that
// directory — see [wifConfig.graphTenantEnabled], which arrived together with
// the token threading rather than before it. ARM cannot: Lighthouse delegates
// SUBSCRIPTION scope only, so a tenant-root ARM call answers about whichever
// directory the credential authenticated in whatever the token names, and
// there is nothing to redirect it to.
//
// A non-federated run is unaffected: an operator's own credential is already
// scoped to their own tenant, which is exactly what they mean to scan.
//
// Known limit, and it cuts BOTH ways, because this keys on the env contract
// rather than on the property it means to detect. Federating into your OWN
// tenant is the benign direction: the credential's default tenant IS the
// scanned tenant, nothing is foreign, and the ARM tenant services are
// suppressed anyway — a real capability loss for a standalone operator, and
// one only [envGraphTenantID] lifts, for the Entra services alone, by naming
// that same tenant. Which is why the CLI help says so rather than blaming
// Lighthouse. The other
// direction is the unsafe one: a credential that IS a Lighthouse managing
// identity but arrives another way — DefaultAzureCredential picking up AZURE_CLIENT_ID plus
// AZURE_CLIENT_SECRET, which is the ordinary way Lighthouse gets automated —
// leaves every guard off. Not reachable from the deployed image, which sets
// the contract or nothing, and subscriptionResourceBatch's filter holds
// either way.
//
// There is now a POSITIVE signal that REACHES the unsafe direction, and it
// does not close it. [bindSubscriptions] asks ARM which directory owns each
// scanned subscription and refuses the scan unless that is the directory
// [envSubscriptionTenantID] names. What it CHECKS is ARM's answer against a
// named directory rather than the env contract; the contract decides only
// whether naming one is MANDATORY. So a managing identity arriving as
// AZURE_CLIENT_ID/AZURE_CLIENT_SECRET is checked as soon as that variable is
// set — and nothing makes that operator set it, since configured() is false.
//
// It binds the SUBSCRIPTIONS, which is not this function's subject. In that
// same state this function returns TRUE, so every tenant-scope service runs
// against whatever directory the ambient credential authenticated in — the
// disclosure above, bound subscriptions or not. Do not read the two guards as
// covering each other: this one is off exactly where that one is opt-in.
// Closing it properly means deciding from ARM rather than from the
// environment — the owner [subscriptionOwners] already returns, compared
// against the credential's own tid — which is a change to what this function
// keys on, not another knob.
//
// [envGraphTenantID] does not close this either: it pins which
// directory answers a Graph token, not that the directory belongs to the
// customer whose subscriptions are being scanned.
func (c wifConfig) tenantScopeEnabled() bool { return !c.configured() }

// graphTenantGUID matches the canonical 8-4-4-4-12 form. Deliberately
// stricter than azidentity's own validTenantID, which also accepts names like
// "organizations" and "common": those are multi-tenant aliases whose meaning
// is "whichever directory the caller signs in to", which is the opposite of
// naming one, and this value's whole job is to name one.
var graphTenantGUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// graphTenantEnabled reports whether the Microsoft Graph scanners may run
// against the directory named by [envGraphTenantID].
//
// Both conditions are required and the conjunction IS the control:
//
//   - federated. A non-federated operator's credential already authenticates
//     in the directory they mean to scan, so [wifConfig.tenantScopeEnabled] is
//     already true and there is nothing to redirect.
//   - the value is a GUID. This also covers "unset", since the empty string
//     does not match — the Graph scanners stay off exactly as before. A
//     malformed value would otherwise reach azidentity's
//     AdditionallyAllowedTenants and policy.TokenRequestOptions.TenantID and
//     fail with a message naming neither the variable nor this scan —
//     locally, from azidentity's own validTenantID, or at the wire with an
//     Entra code, depending on which characters it carries.
//
// This ungates the GRAPH scanners alone. It never reopens the ARM tenant
// phase, whose calls Lighthouse does not delegate and which no token can
// redirect.
//
// Being true is NOT sufficient for the calls to be safe, and the other two
// halves live elsewhere on purpose: the credential must carry this directory
// in its allow list ([newFederatedCredential]) and every Graph token must name
// it ([graphClient.get]). azidentity's resolveTenant returns the credential's
// DEFAULT tenant whenever a request specifies none, so a gate opened without
// the threading would read DISCO'S directory and write it into the customer's
// inventory — the disclosure of tenantScopeEnabled's doc, switched on by a
// variable promising the opposite. That is why the knob was withheld until the
// threading existed, and why scanEntra additionally REFUSES a token whose tid
// is not this value: the env var says which directory was consented, the tid
// says which one actually answered, and only the second is evidence.
func (c wifConfig) graphTenantEnabled() bool {
	return c.configured() && graphTenantGUID.MatchString(c.graphTenantID)
}

// credentialMode names which credential a scan authenticates with.
type credentialMode string

const (
	// credModeFederated exchanges an AWS STS web identity token for an Entra
	// token via a federated identity credential.
	credModeFederated credentialMode = "federated"
	// credModeDefault is azidentity's DefaultAzureCredential chain.
	credModeDefault credentialMode = "default"
)

// selectCredentialMode decides how to authenticate. Pure and side-effect-free
// so the precedence is unit-testable without touching AWS, Entra or the
// environment — the same contract as resolveSubscriptionScope.
func selectCredentialMode(c wifConfig) credentialMode {
	if c.configured() {
		return credModeFederated
	}
	return credModeDefault
}

// newAzureCredential builds the credential for the selected mode.
func newAzureCredential(ctx context.Context, c wifConfig) (azcore.TokenCredential, error) {
	if c.partiallyConfigured() {
		return nil, ErrIncompleteWIFConfig
	}
	if selectCredentialMode(c) == credModeFederated {
		return newFederatedCredential(ctx, c)
	}
	// DefaultAzureCredential tries env vars -> workload identity -> Azure CLI.
	return azidentity.NewDefaultAzureCredential(nil)
}

// newFederatedCredential builds the AWS-to-Entra federated credential.
//
// AdditionallyAllowedTenants carries the consented customer directory and
// NOTHING else — never "*", which azidentity accepts and which would let any
// future caller mint a token for any directory this application is registered
// in. With the list empty, azidentity's resolveTenant refuses a request naming
// a tenant other than c.tenantID outright (azidentity.go, resolveTenant: a
// non-empty default plus an empty list falls through to an error), so the
// threading in [graphClient.get] would fail at every Graph call rather than
// silently reading the wrong directory. Both halves are required and neither
// is useful alone.
func newFederatedCredential(ctx context.Context, c wifConfig) (azcore.TokenCredential, error) {
	assertion, err := webIdentityAssertion(ctx, c)
	if err != nil {
		return nil, err
	}
	// UNTESTED CALL SITE, stated rather than papered over: reverting this
	// argument to nil is green across the whole suite. credentialOptions is
	// asserted in isolation and graphClient's threading is asserted at the
	// token, but nothing reaches HERE without AWS STS, so the two halves are
	// each pinned and their JOINING is not. The failure is fail-closed and
	// loud — every Graph acquisition would error, because azidentity refuses a
	// named tenant that is not in the list — which is why it is recorded
	// rather than propped up with a seam built only for a test.
	cred, err := azidentity.NewClientAssertionCredential(c.tenantID, c.clientID, assertion, credentialOptions(c))
	if err != nil {
		return nil, fmt.Errorf("azure wif: client assertion credential: %w", err)
	}
	return &retryCredential{inner: cred}, nil
}

// credentialOptions builds the azidentity options for the federated
// credential. Pure and side-effect-free so the allow list can be asserted
// without AWS, Entra or the environment — the same contract as
// [selectCredentialMode] and [resolveSubscriptionScope].
//
// Returns nil (azidentity's defaults, no allow list) unless a customer
// directory is configured, so an ordinary Lighthouse scan keeps a credential
// that can mint tokens for disco's tenant and nothing else.
//
// The list holds exactly the consented directory. Never "*": azidentity
// accepts it and it would let any caller, now or later, mint a token for any
// directory this multi-tenant application has a service principal in — which
// is every customer who has ever deployed the Lighthouse offer.
func credentialOptions(c wifConfig) *azidentity.ClientAssertionCredentialOptions {
	if !c.graphTenantEnabled() {
		return nil
	}
	return &azidentity.ClientAssertionCredentialOptions{
		AdditionallyAllowedTenants: []string{c.graphTenantID},
	}
}

// webIdentityAssertion wires the AWS side of the exchange: the subject
// identity, the regional STS client, and the callback that mints one
// assertion per token request.
func webIdentityAssertion(ctx context.Context, c wifConfig) (func(context.Context) (string, error), error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("azure wif: load aws config: %w", err)
	}
	// Refuse an unregioned config here rather than letting endpoint resolution
	// fail lazily inside MSAL: that arrives wrapped as an Entra authentication
	// failure, once per service, naming neither AWS nor the missing variable.
	// Redaction deliberately leaves this message alone for the same reason —
	// see redactCredentialError.
	if awsCfg.Region == "" {
		return nil, errors.New("azure wif: no AWS region resolved; set AWS_REGION")
	}
	creds, err := wifSubjectCredentials(awsCfg, c)
	if err != nil {
		return nil, err
	}
	awsCfg.Credentials = creds
	// Regional client, from the region checked above — which is all endpoint
	// resolution needs. Not operation-specific: sts/endpoints.go dispatches on
	// region and the FIPS/dual-stack/global flags and names no operation, so
	// GetWebIdentityToken resolves exactly like AssumeRole. (AWS's docs say the
	// global endpoint does not serve this call. Unverified here, and nothing
	// depends on it.)
	return assertionCallback(sts.NewFromConfig(awsCfg), c.effectiveAudience()), nil
}

// webIdentityTokenAPI is the one STS call the assertion needs, named so the
// callback can be exercised without AWS.
type webIdentityTokenAPI interface {
	GetWebIdentityToken(context.Context, *sts.GetWebIdentityTokenInput, ...func(*sts.Options)) (*sts.GetWebIdentityTokenOutput, error)
}

// assertionCallback returns the callback azidentity invokes for a fresh client
// assertion. The callback must be safe for concurrent use — azidentity's
// NewClientAssertionCredential requires it — which holds here because the
// closure captures only an immutable audience and a concurrency-safe STS
// client. Do not read azidentity as the guarantee: MSAL invokes the callback
// with no lock at all, and azidentity's confidentialClient holds TWO token
// mutexes (caeMu and noCAEMu, picked by TokenRequestOptions.EnableCAE), which
// therefore do not exclude each other. What actually collapses concurrent
// misses in THIS program is cachingCredential's singleflight one layer up —
// but that is our own code and could change, so the callback carries the
// requirement itself.
func assertionCallback(client webIdentityTokenAPI, audience string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		out, err := client.GetWebIdentityToken(ctx, &sts.GetWebIdentityTokenInput{
			Audience:         []string{audience},
			SigningAlgorithm: aws.String(webIdentitySigningAlg),
			DurationSeconds:  aws.Int32(webIdentityTokenTTL),
		})
		if err != nil {
			return "", fmt.Errorf("azure wif: get web identity token: %w", err)
		}
		if out == nil || out.WebIdentityToken == nil || *out.WebIdentityToken == "" {
			return "", errors.New("azure wif: sts returned an empty web identity token")
		}
		return *out.WebIdentityToken, nil
	}
}

// wifSubjectCredentials resolves the AWS identity whose ARN the Entra trust
// names: the ambient one, or an AssumeRole session.
//
// A half-set session is an error, not a fall back to the ambient identity. The
// federated credential's subject must match the presented `sub` claim exactly
// — Entra supports no wildcards — so presenting a different principal fails
// the exchange with AADSTS70021, which is indistinguishable from the
// propagation delay [retryCredential] exists to ride out. Failing here names
// the real cause instead.
func wifSubjectCredentials(awsCfg aws.Config, c wifConfig) (aws.CredentialsProvider, error) {
	if !c.sessionRequested() {
		return awsCfg.Credentials, nil
	}
	if !c.sessionComplete() {
		return nil, fmt.Errorf("azure wif: %s and %s must be set together", envWIFRoleARN, envWIFSessionName)
	}
	// CredentialsCache re-assumes on expiry — a scan outlives the one-hour
	// ceiling on an AssumeRole session.
	return aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(
		sts.NewFromConfig(awsCfg), c.roleARN,
		func(o *stscreds.AssumeRoleOptions) { o.RoleSessionName = c.sessionName },
	)), nil
}

// retryCredential rides out federated-credential propagation.
//
// Microsoft documents AADSTS70021 ("No matching federated identity record
// found for presented assertion") as an EXPECTED transient after a federated
// credential is configured, because the authorization service's directory
// cache replicates per region, and recommends retrying every request even
// after one has already succeeded — a later request can still land on a node
// with stale data.
//
// Deliberately narrow: only that error code is retried. A genuinely wrong
// subject, audience or tenant produces the same code but never stops, so the
// bounded attempt count is what keeps a misconfiguration from presenting as a
// hang.
//
// The attempt cap bounds ONE acquisition, which is not the same as bounding a
// scan. cachingCredential (azure_credential.go) caches successes and never
// failures, and its singleflight coalesces only CONCURRENT misses — so under a
// permanent misconfiguration each later wave pays the full backoff again,
// across a scan that is going to fail regardless.
//
// So the retry also gives up eventually. The give-up point is WALL CLOCK since
// the first such failure, not one exhausted acquisition: the attempts sleep
// 1s+2s+3s, and Microsoft describes replication in minutes, so latching on one
// exhaustion would abandon precisely the window this exists for — a credential
// that becomes valid at t+30s would fail the whole scan. Past
// federationPropagationBudget the code is treated as permanent, which it then
// almost certainly is, and later acquisitions report it immediately.
//
// Not safe for concurrent use by value — take a pointer. The zero value is
// usable once inner is set.
type retryCredential struct {
	inner azcore.TokenCredential
	// firstFailure is the unix-nano time of the first propagation failure, 0
	// until one happens.
	firstFailure atomic.Int64
	// budget overrides federationPropagationBudget. Tests set it so the
	// give-up can be exercised without waiting out the real window.
	budget time.Duration
	// delay overrides the backoff unit. Zero means
	// [federationPropagationDelay]; tests set it so the attempt cap can be
	// exercised without spending the real replication window.
	delay time.Duration
}

// backoffUnit is the configured delay, or the production default.
func (r *retryCredential) backoffUnit() time.Duration {
	if r.delay > 0 {
		return r.delay
	}
	return federationPropagationDelay
}

// budgetOrDefault is the configured budget, or the production default.
func (r *retryCredential) budgetOrDefault() time.Duration {
	if r.budget > 0 {
		return r.budget
	}
	return federationPropagationBudget
}

// exhausted reports whether this credential has been failing to replicate for
// longer than the budget, and records the first failure of the current streak
// if none was recorded.
//
// The budget measures the CURRENT streak, which is why [retryCredential.GetToken]
// clears the stamp on success. Left uncleared it would be an absolute deadline
// per credential: one transient failure that resolved on its retry would arm
// it, and every propagation error more than a budget later would get a single
// attempt — abandoning exactly the "a later request can still land on a node
// with stale data" case this whole type exists for.
func (r *retryCredential) exhausted(now time.Time) bool {
	if r.firstFailure.CompareAndSwap(0, now.UnixNano()) {
		return false
	}
	first := r.firstFailure.Load()
	if first == 0 {
		// A concurrent success cleared the stamp between the swap and this
		// load. Reading 0 as a time is 1970, which exceeds any budget and
		// would abandon a retry this acquisition is entitled to. The streak
		// ended; start a new one on the next failure.
		//
		// Race-only and deliberately uncovered: CompareAndSwap fails only on a
		// non-zero value, so single-threaded this branch is unreachable and no
		// test can kill a mutant that deletes it. It used to be unreachable in
		// production as well, because cachingCredential's singleflight serialises
		// acquisitions per scope and federation requested only one. The Graph
		// threading ended that: tokenCacheKey is TenantID + "\x00" + scopes, so a
		// consented directory puts the Entra phase on (customer, graphScope)
		// while the per-subscription fan-out is on ("", armScope) — two keys, two
		// concurrent acquisitions, one retryCredential. The branch is benign
		// either way, which is why it was written down rather than left for this
		// change to discover.
		return false
	}
	return now.Sub(time.Unix(0, first)) > r.budgetOrDefault()
}

// federationPropagationAttempts / federationPropagationDelay bound ONE
// acquisition; federationPropagationBudget bounds how long a credential keeps
// retrying across acquisitions. Four attempts sleeping 1s + 2s + 3s is short
// enough that a scan is not held up; two minutes is long enough for the
// replication Microsoft describes, and past it a repeating AADSTS70021 is a
// wrong subject, audience or tenant rather than a stale cache.
const (
	federationPropagationAttempts = 4
	federationPropagationDelay    = 1 * time.Second
	federationPropagationBudget   = 2 * time.Minute
)

// GetToken delegates to inner, retrying only AADSTS70021 with linear backoff
// until the propagation budget is spent. See [retryCredential].
func (r *retryCredential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	var tok azcore.AccessToken
	var err error
	for attempt := 1; attempt <= federationPropagationAttempts; attempt++ {
		tok, err = r.inner.GetToken(ctx, opts)
		if err == nil {
			// The streak is per-CREDENTIAL, not per-scope, and the Graph
			// threading made that observable: a succeeding ARM acquisition now
			// clears the stamp a concurrently-failing Graph one set, so the
			// give-up budget can be extended indefinitely against a permanently
			// broken consent. Bounded by federationPropagationAttempts either
			// way — a few seconds per acquisition, and cachingCredential means
			// one acquisition per (tenant, scope) — so it is left per-credential
			// rather than given a key: the budget exists to stop a scan hanging
			// on propagation, and a scan whose ARM half is working is not hung.
			r.firstFailure.Store(0)
			return tok, nil
		}
		if !isFederationPropagationError(err) {
			return tok, err
		}
		if r.exhausted(time.Now()) || attempt == federationPropagationAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return azcore.AccessToken{}, ctx.Err()
		case <-time.After(time.Duration(attempt) * r.backoffUnit()):
		}
	}
	return azcore.AccessToken{}, err
}

// isFederationPropagationError reports whether err is Entra's
// not-yet-replicated federated credential error.
//
// Matched with its trailing colon, which is how Entra formats the code. The
// bare prefix would otherwise also match AADSTS700213 — the permanent
// subject-mismatch failure, whose message text is nearly identical — and any
// other code sharing those six digits. Retrying a permanent failure only
// delays the report of it.
func isFederationPropagationError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "AADSTS70021:")
}
