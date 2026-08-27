package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEntraUser, Service: "graph", Uncatalogued: true})
	registerType(restype.Descriptor{Type: TypeEntraGroup, Service: "graph", Uncatalogued: true})
	registerType(restype.Descriptor{Type: TypeEntraServicePrincipal, Service: "graph", Uncatalogued: true})
	registerType(restype.Descriptor{Type: TypeEntraApplication, Service: "graph", Uncatalogued: true})
	// Entra ID types are real identities scanned via Microsoft Graph; ARM
	// Providers/List can't see them (Graph isn't an ARM RP), so uncatalogued
	// rather than synthetic.
	registerTenantService(tenantServiceEntry{
		name:        "azure:microsoft.entra",
		fn:          scanEntra,
		graphScoped: true,
	})
}

const (
	graphScope      = "https://graph.microsoft.com/.default"
	graphDefaultURL = "https://graph.microsoft.com/v1.0"
	graphPageSize   = 500
	// armScope is the ARM token audience — resolves the tenant GUID from the
	// `tid` claim without needing Graph access; every scan already holds an
	// ARM token.
	armScope = "https://management.azure.com/.default"
)

// tokenIssuer narrows azcore.TokenCredential to the one method scanEntra
// uses, letting tests stub token issuance without a full azidentity
// credential graph.
type tokenIssuer interface {
	GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error)
}

// graphClient is a tiny REST client for the four Microsoft Graph list
// endpoints disco scans. Replaces msgraph-sdk-go (kiota-generated, ~9MB
// symbols + 88 transitive subpkgs) — the curated *Attrs structs already
// model Graph's JSON shape, so the SDK's discriminator-driven model graph
// was pure overhead.
type graphClient struct {
	cred tokenIssuer
	http *http.Client
	// tenantID names the DIRECTORY every token this client mints is for.
	// Empty means "the credential's own", which is the unfederated case and
	// what azidentity does when a request specifies no tenant. Non-empty is
	// how a Lighthouse-federated scan reads the CUSTOMER's directory instead
	// of disco's — see wifConfig.graphTenantEnabled, and note the credential
	// must also carry this directory in AdditionallyAllowedTenants or every
	// call here fails at token acquisition rather than reading the wrong one.
	tenantID string
	baseURL  string
}

// newGraphClient returns a client for the default Graph endpoint. tenantID
// names the DIRECTORY every token it mints is for; empty means the
// credential's own, which is the unfederated case. A non-empty value must also
// appear in the credential's AdditionallyAllowedTenants — see
// [wifConfig.graphTenantEnabled] — or every call fails at token acquisition.
func newGraphClient(cred tokenIssuer, tenantID string) *graphClient {
	return &graphClient{cred: cred, http: graphHTTPClient, baseURL: graphDefaultURL, tenantID: tenantID}
}

// maxGraphErrorBody caps the error body read from a Graph response. The body
// reaches the customer's append-only scan record through graphErr, so it is
// bounded twice: here, so it never sits in memory unbounded, and again at
// reportEntraErr's chokepoint, which bounds every message however it was
// built.
const maxGraphErrorBody = 8 << 10

// maxGraphPageBody caps one collection page. Far above any real page (Graph
// pages these collections in the hundreds of objects), so it is a backstop
// against a server that answers a paging request with an endless body rather
// than a tuning knob.
const maxGraphPageBody = 64 << 20

// maxGraphPages caps how many pages one collection may take. 200k is orders of
// magnitude past any real directory, so it is a backstop against a server that
// pages forever, not a tuning knob.
//
// It bounds the COUNT of iterateGraph's cycle-detection keys and NOT their
// size: a key is a whole nextLink, nothing caps a nextLink's length, and the
// link is read from a body capped at maxGraphPageBody — so the map's worst
// case is the product of the two, not this number. Within the threat model
// iterateGraph names (a compromised or tampered graph.microsoft.com, never the
// customer), a few hundred pages carrying multi-megabyte $skiptokens exhaust
// memory long before the page count does. Open, and stated rather than implied
// so this constant is not read as closing it.
//
// A var rather than a const only so a test can lower it: at the real value the
// refusal costs 200k round trips to observe, and a test that instead re-derived
// the bound would agree with itself whether or not iterateGraph still consults
// it. Nothing outside a test writes to it.
var maxGraphPages = 200_000

// graphHTTPClient is the Graph client. It shares the ARM pool's TRANSPORT —
// the connection pooling is the point of that variable — but not its
// http.Client, because it must refuse REDIRECTS and ARM must not.
//
// sameGraphHost checks the @odata.nextLink in a response BODY and nothing
// else, so a 3xx was the unguarded half of the same hazard, and the worse
// half: measured, a 302 from the Graph host to another origin had the foreign
// response DECODED and stored as the customer's directory objects, with the
// bearer forwarded to that origin.
//
// net/http's own rule is LOOSER than "same host", which is the part worth
// getting right: shouldCopyHeaderOnRedirect calls isDomainOrSubdomain, so
// Authorization is kept for a SUBDOMAIN too — graph.microsoft.com to
// evil.graph.microsoft.com forwards a Directory.Read.All token. It reads
// url.Hostname(), so scheme and port are ignored as well, and an https to http
// downgrade on the same host puts that token on the wire in clear — the shape
// sameGraphHost refuses by name for a nextLink.
//
// Refusing every redirect is therefore the policy, and the cost is not quite
// zero: a 3xx from Graph on these collections is not a documented shape, but a
// 302 with no Location on GET /v1.0/users has been reported, and net/http
// carries its own comment about having seen 3xx-without-Location in the wild.
// A refusal there is a loud, correct-direction failure rather than a silent
// one, which is the trade being made.
//
// ErrUseLastResponse rather than an error from CheckRedirect:
// it hands the 3xx back for get to classify by TYPE, where an error would
// arrive as a *url.Error whose text carries the server's own Location.
var graphHTTPClient = &http.Client{
	Transport: azHTTPClient.Transport,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// graphErr surfaces Graph error bodies so reportEntraErr's substring
// classifier (Authorization_RequestDenied, Insufficient privileges, 401,
// 403) still matches against raw HTTP responses.
type graphErr struct {
	status int
	body   string
}

func (e *graphErr) Error() string {
	return fmt.Sprintf("graph: %d: %s", e.status, e.body)
}

// graphPage is the Graph collection envelope. Value is the per-call slice
// of entities; @odata.nextLink carries forward when results are paginated.
type graphPage[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

func (g *graphClient) get(ctx context.Context, fullURL string, out any) error {
	tok, err := g.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}, TenantID: g.tenantID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return newGraphTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// A 3xx with NO Location is a real shape, not a hypothetical: net/http
		// carries its own comment about having observed it, and Graph has been
		// reported answering GET /v1.0/users that way. hostOnly("") would call
		// it a relative link, which is a claim about a header that is absent.
		loc := resp.Header.Get("Location")
		host := "(none; the response sent no Location)"
		if loc != "" {
			host = hostOnly(loc)
		}
		return &redirectRefusedError{status: resp.StatusCode, host: host}
	}
	if resp.StatusCode >= 400 {
		// Capped: the body is the remote side's to choose, and an uncapped
		// ReadAll here would put an arbitrary number of bytes in memory before
		// anything downstream had a chance to bound them. The cap is generous
		// against a real Graph error body, whose useful part is the leading
		// "error.code"/"error.message" object.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxGraphErrorBody))
		return &graphErr{status: resp.StatusCode, body: string(body)}
	}
	// The success body was uncapped while the ERROR body was capped, which is
	// the wrong way round: this is the path that decodes into memory AND keeps
	// going for another page. The deadline runTenantServices applies bounds
	// TIME, not bytes, so it is not a substitute.
	//
	// Constructed as a *io.LimitedReader rather than through io.LimitReader for
	// the HANDLE, not for different behaviour: io.LimitReader RETURNS a
	// *LimitedReader (io/io.go), so the two truncate identically. What the
	// handle buys is reading N back. TRUNCATION-shaped corruption reaches
	// json.Decoder as io.ErrUnexpectedEOF exactly as hitting the cap does, so
	// the two are indistinguishable from the error alone; a syntactically
	// invalid body arrives as *json.SyntaxError instead and was never
	// confusable. N is seeded at the cap PLUS ONE, so N == 0 means strictly
	// more than maxGraphPageBody bytes were delivered and the refusal's stated
	// limit is always literally true — a body of exactly the cap leaves N == 1
	// and cannot reach this branch. What it does NOT establish is that the
	// size is WHY the decode failed: an over-cap body that was also malformed
	// lands here and is reported as too large. Do not remove the +1 to "fix"
	// that; the +1 is what makes the size claim true.
	lr := &io.LimitedReader{R: resp.Body, N: maxGraphPageBody + 1}
	if derr := json.NewDecoder(lr).Decode(out); derr != nil {
		if lr.N <= 0 {
			return &oversizePageError{limit: maxGraphPageBody}
		}
		return derr
	}
	return nil
}

// iterateGraph paginates via @odata.nextLink, calling fn for every entity.
// Returning false from fn stops iteration. nextLink is absolute when present,
// so it is used verbatim apart from the host check below.
//
// The host check is not redundant with the stdlib's. A nextLink is a fresh
// REQUEST, not a redirect, so net/http's rule about dropping Authorization on
// a cross-host redirect never applies to it — g.get would attach the bearer to
// whatever host the response body named. That was already true before the
// directory could be a customer's; what changed is the value of the token, now
// a customer's directory under Directory.Read.All rather than disco's own. The
// body is Microsoft's, so this is a defence against a Graph compromise or a
// response tampered with in transit, not against the customer.
func iterateGraph[T any](ctx context.Context, g *graphClient, startURL string, fn func(T) bool) error {
	// A nextLink is the server's to choose, including choosing one already
	// visited. Refusing an exact REPEAT rather than capping the page count: a
	// cap is a number that truncates a large directory the day it is wrong.
	//
	// The justification is local, NOT Microsoft's. An earlier version cited
	// learn.microsoft.com/graph/paging for "a repeat is never legitimate" and
	// that page says something close to the opposite: its
	// DirectoryPageTokenNotFoundException guidance has a client RE-USE the last
	// successful nextLink after a failed page, so the same value appears twice
	// in a supported flow. What makes a repeat illegitimate HERE is that
	// iterateGraph never retries a page — one get, then forward — so within one
	// call the same link twice can only be the server looping us. Implement a
	// page-level retry and this guard has to move with it.
	//
	// This is a PRECISE diagnosis, NOT the loop's bound, and reading it as the
	// bound was a defect for one round: a server that increments $skiptoken
	// forever produces a new URL every time and walks straight past it. What
	// bounds the loop is the serviceTimeout runTenantServices now applies —
	// tenant services had none, while every (subscription, service) pair
	// already did.
	seen := map[string]bool{}
	cur := startURL
	for cur != "" {
		if seen[cur] {
			return &repeatedLinkError{host: hostOnly(cur)}
		}
		if len(seen) >= maxGraphPages {
			return &tooManyPagesError{limit: maxGraphPages}
		}
		seen[cur] = true
		var page graphPage[T]
		if err := g.get(ctx, cur, &page); err != nil {
			return err
		}
		for _, item := range page.Value {
			if !fn(item) {
				return nil
			}
		}
		if page.NextLink != "" && !sameGraphHost(g.baseURL, page.NextLink) {
			return &foreignLinkError{host: hostOnly(page.NextLink)}
		}
		cur = page.NextLink
	}
	return nil
}

// graphTransportError reports a Graph request that never produced a response.
//
// It exists to keep the REQUEST URL out of the error TEXT. http.Client.Do
// returns a *url.Error whose Error() embeds the full URL, and from page two
// onward that URL's path and query come from the response body — sameGraphHost
// constrains the scheme and the host and nothing else. reportEntraErr's
// substring classifier would then be reading attacker-influenced text: a
// nextLink path of "/v1.0/Authorization_RequestDenied" demotes a transport
// failure to the missing-consent WARNING. Measured.
//
// Unwrapping the *url.Error removes the layer that renders the full URL, and
// that is ALL it does — the inner error is not intrinsically safe. net/http
// parses the Location header BEFORE consulting CheckRedirect
// (net/http/client.go: the parse and its uerr(...) precede the
// ErrUseLastResponse check), and on a parse failure the inner error is
// fmt.Errorf("failed to parse Location header %q: %v", loc, err) — where loc
// is the server's own string. So graphHTTPClient's redirect refusal cannot
// keep response-controlled text out of this type, and neither can the unwrap.
//
// What keeps it out of reportEntraErr's substring classifier is
// neverAConsentFailure holding *graphTransportError, and nothing else. Do not
// remove it from that set on the strength of this unwrap. Unwrap is kept so
// callers testing errors.Is(err, context.Canceled) still see through it.
type graphTransportError struct{ err error }

// newGraphTransportError wraps a transport failure, unwrapping the *url.Error
// layer that carries the URL.
func newGraphTransportError(err error) *graphTransportError {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return &graphTransportError{err: ue.Err}
	}
	return &graphTransportError{err: err}
}

func (e *graphTransportError) Error() string {
	return "graph: request failed: " + e.err.Error()
}

func (e *graphTransportError) Unwrap() error { return e.err }

// sanitizeForScanRecord bounds and de-fangs remote text on its way to
// store.ScanError. Two callers: reportEntra, for Graph-derived text, and
// reportPanic, for a recovered panic value.
//
// EVERY message reaching that record carries bytes somebody else chose — a
// Graph error body, a nextLink host, a Location host, a transport cause that
// embeds a response header line (measured: "malformed MIME header line: <that
// line>"). Classifying by TYPE stops that text steering a decision; this stops
// it filling an append-only record, and stops it lying about the sentence it
// sits in.
//
// Two passes, and both are needed. Bidi controls SURVIVE url.Parse — measured,
// hostOnly("https://‮evil.example/x") returns the override intact — and
// visually reorder the text around them, so a host can make the refusal read
// as something else; ASCII control characters are already refused by
// url.Parse, so this is not about terminal escapes. The rune cap is second, so
// a replacement can never be split.
func sanitizeForScanRecord(s string) string {
	const max = 200
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '\uFFFD'
	}, s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "… (truncated)"
}

// oversizePageError reports a collection page larger than maxGraphPageBody.
type oversizePageError struct{ limit int64 }

func (e *oversizePageError) Error() string {
	return fmt.Sprintf("graph: refusing a response body over %d bytes", e.limit)
}

// tooManyPagesError reports a collection that paged past maxGraphPages.
//
// The deadline in runTenantServices is the primary bound on a hostile paging
// loop; this is the memory half, because the cycle check below must remember
// every link it has seen and the server chooses those strings.
type tooManyPagesError struct{ limit int }

func (e *tooManyPagesError) Error() string {
	return fmt.Sprintf("graph: refusing to page past %d responses for one collection", e.limit)
}

// repeatedLinkError reports a pagination link the scan has already followed.
//
// Its own TYPE for the same reason as its two neighbours: the link is the
// server's to choose, so its text must never reach a classifier.
type repeatedLinkError struct{ host string }

func (e *repeatedLinkError) Error() string {
	return "graph: refusing to follow a pagination link already visited in this scan (host " + e.host + ")"
}

// redirectRefusedError reports a Graph response that tried to send the request
// somewhere else. See graphHTTPClient for what following one costs.
//
// Its own TYPE for the same reason as foreignLinkError: the Location header is
// the server's to choose, so its text must never reach a classifier. Only the
// host is carried, and only through a fixed prefix.
type redirectRefusedError struct {
	status int
	host   string
}

func (e *redirectRefusedError) Error() string {
	return fmt.Sprintf("graph: refusing to follow a %d redirect (Location host %s)", e.status, e.host)
}

// foreignLinkError reports a pagination link that left the configured Graph
// origin. Its own TYPE, not a fmt.Errorf, because reportEntraErr classifies by
// SUBSTRING over err.Error() and the attacker chooses half of any text taken
// from the link: a nextLink of "https://evil.example/Authorization_RequestDenied"
// echoed verbatim demotes this refusal to the routine missing-consent warning,
// making the strongest evidence of a tampered Graph response indistinguishable
// from a permission the customer simply has not granted. Only the HOST is
// carried, and even that is reported through a fixed prefix.
//
// Callers test it with errors.Is/As, never by reading the message.
type foreignLinkError struct{ host string }

func (e *foreignLinkError) Error() string {
	return "graph: refusing to follow a pagination link that left the configured Graph host (saw host " + e.host + ")"
}

// hostOnly returns the host of a URL. Never the path or query, which is where
// a hostile link would put text chosen to steer a classifier — though note the
// HOST can carry such text too ("[fe80::1% 401]" is a legal one), so this
// bounds what a remote side writes onto the customer's scan record, not what
// it can steer. What stops it steering is reportEntraErr resolving the TYPE,
// before formatAzureError; see the ordering comment there.
//
// Two distinct placeholders, because they are different diagnoses: a link that
// will not parse at all, and one that parses with no host. The second is
// worded for the relative link that is its realistic cause and which Graph is
// not supposed to emit — "https:/v1.0/users" and "mailto:" reach it too, and
// are refused identically.
func hostOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable)"
	}
	if u.Host == "" {
		return "(no host; the link was relative)"
	}
	return u.Host
}

// sameGraphHost reports whether next is an absolute URL on the same ORIGIN as
// base — same scheme and same host.
//
// Compared by parsed HOST, never by string prefix:
// "https://graph.microsoft.com.evil.example" carries the right prefix and is a
// different server. A relative link answers false: Graph documents nextLink as
// absolute, so a relative one is not a shape to accommodate.
//
// The scheme is compared against BASE rather than required to be https, which
// is the same rule stated once instead of twice. In production baseURL is
// https, so an http nextLink is refused — a downgrade that would put the
// bearer on the wire in clear. Against an httptest server baseURL is http and
// the server's own links are followed, so the check needs no test-only escape
// hatch, which is the kind of hatch that later turns out to be reachable.
func sameGraphHost(base, next string) bool {
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	n, err := url.Parse(next)
	if err != nil {
		return false
	}
	// ASCII-only comparison, deliberately not strings.EqualFold: that applies
	// UNICODE simple folding, where U+017F (long s) folds to 's', and
	// url.Parse passes any byte >= 0x80 into Host without validating it. So
	// EqualFold would answer true for "graph.microſoft.com", a different name
	// that a hostile nextLink can spell. Reject a non-ASCII host outright and
	// fold only ASCII case.
	return n.Scheme == b.Scheme && asciiHostEqual(n.Host, b.Host)
}

// asciiHostEqual reports whether two hosts are equal ignoring ASCII case, with
// any non-ASCII byte on either side answering false. A punycode host is
// already ASCII and compares normally.
//
// Against an ASCII base the non-ASCII test is belt to the byte comparison's
// braces — a byte >= 0x80 cannot equal any byte of an ASCII host, and a UTF-8
// homoglyph usually differs in LENGTH as well (graph.microſoft.com is 20 bytes
// against 19). It is kept because it states the intent for a base this
// function does not currently receive, and because "usually" is not a property
// to rely on. What must NOT be claimed is that this branch is what defeats the
// homoglyph; the comparison itself is.
func asciiHostEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if x >= 0x80 || y >= 0x80 {
			return false
		}
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// graphURL builds a Graph list-endpoint URL with $select + $top set so the
// response carries only curated attribute fields and pages predictably.
func (g *graphClient) listURL(path string, fields []string) string {
	q := url.Values{}
	q.Set("$select", strings.Join(fields, ","))
	q.Set("$top", fmt.Sprintf("%d", graphPageSize))
	return g.baseURL + "/" + path + "?" + q.Encode()
}

// scanEntra discovers Entra ID (Azure AD) directory objects via Microsoft
// Graph: users, groups, service principals, and application registrations.
// Uses raw REST against graph.microsoft.com — the curated *Attrs structs
// below model exactly the JSON keys requested via $select.
//
// Tenant scope: NativeID is the object's `id` GUID; AccountID is the tenant
// ID resolved from the JWT `tid` claim of a Graph token. Closure is empty —
// these objects sit above the subscription/RG hierarchy.
//
// Permission failures (Directory.Read.All not granted) degrade to one scan
// warning and a partial scan; later sub-scope scanners proceed normally.
func scanEntra(ctx context.Context, subs []subscription, cred azcore.TokenCredential, wif wifConfig, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(subs) == 0 {
		return 0, 0, nil
	}
	// graphTenant is the directory every token below names. Empty is the
	// unfederated case — azidentity then resolves the credential's own, which
	// is the directory that operator meant to scan. Non-empty is a federated
	// scan pointed at a consented CUSTOMER directory.
	var graphTenant string
	if wif.graphTenantEnabled() {
		graphTenant = wif.graphTenantID
	}
	// Resolved from a token minted for graphTenant, NOT from the credential's
	// default — reading the default here would label the customer's rows with
	// disco's directory the moment the pin worked.
	tenantID, terr := tenantIDFromCredScopeTenant(ctx, cred, graphScope, graphTenant)
	if terr != nil {
		reportEntra(st, tenantScopeLabel(subs), "could not resolve tenant id: "+formatAzureError(terr), true)
		return 0, 0, nil
	}
	// The env var says which directory was consented; the tid says which one
	// actually answered. Only the second is evidence, and they can disagree
	// only if something upstream of this process is wrong — so refuse rather
	// than write. Every row this phase stores is keyed by tenantID, so a
	// mismatch that proceeded would file one directory's identities under
	// another's account id, which is the disclosure the whole gate exists to
	// prevent and is not correctable after the fact.
	if graphTenant != "" && !strings.EqualFold(tenantID, graphTenant) {
		reportEntra(st, tenantScopeLabel(subs), "refusing to store directory objects: the Graph token was issued for a different directory than the one this scan was configured for", true)
		return 0, 0, nil
	}
	g := newGraphClient(cred, graphTenant)

	t, n := scanEntraUsers(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraGroups(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraServicePrincipals(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	t, n = scanEntraApplications(ctx, g, tenantID, st, scanID)
	total += t
	inserted += n
	return total, inserted, nil
}

// userAttrs is the curated subset persisted under attributes for a Graph
// User — stable shape so resolvers can index by upn / object id without
// depending on the full payload. Field tags match Graph JSON keys 1:1 —
// same struct doubles as the unmarshal target for the REST response.
type userAttrs struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	UserPrincipalName string `json:"userPrincipalName,omitempty"`
	Mail              string `json:"mail,omitempty"`
	AccountEnabled    *bool  `json:"accountEnabled,omitempty"`
	UserType          string `json:"userType,omitempty"`
}

func scanEntraUsers(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("users", []string{"id", "displayName", "userPrincipalName", "mail", "accountEnabled", "userType"})
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			reportEntra(st, "users", formatAzureError(uerr), false)
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	err := iterateGraph(ctx, g, startURL, func(u userAttrs) bool {
		if u.ID == "" {
			return true
		}
		name := u.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraUser, NativeID: u.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(u),
			DiscoveredBy: scanID,
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
		reportEntraErr(st, "users", err)
	}
	flush()
	return total, inserted
}

type groupAttrs struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName"`
	Description     string   `json:"description,omitempty"`
	MailEnabled     *bool    `json:"mailEnabled,omitempty"`
	SecurityEnabled *bool    `json:"securityEnabled,omitempty"`
	GroupTypes      []string `json:"groupTypes,omitempty"`
}

func scanEntraGroups(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("groups", []string{"id", "displayName", "description", "mailEnabled", "securityEnabled", "groupTypes"})
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			reportEntra(st, "groups", formatAzureError(uerr), false)
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	err := iterateGraph(ctx, g, startURL, func(gr groupAttrs) bool {
		if gr.ID == "" {
			return true
		}
		name := gr.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraGroup, NativeID: gr.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(gr),
			DiscoveredBy: scanID,
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
		reportEntraErr(st, "groups", err)
	}
	flush()
	return total, inserted
}

type spAttrs struct {
	ID                     string `json:"id"`
	AppID                  string `json:"appId,omitempty"`
	DisplayName            string `json:"displayName"`
	ServicePrincipalType   string `json:"servicePrincipalType,omitempty"`
	AccountEnabled         *bool  `json:"accountEnabled,omitempty"`
	AppOwnerOrganizationID string `json:"appOwnerOrganizationId,omitempty"`
}

// microsoftFirstPartyTenants are the Entra tenants Microsoft uses to host
// first-party / built-in service principals (Microsoft Graph, Azure CLI,
// Office 365 apps, etc.). A service principal whose appOwnerOrganizationId
// matches is provider-managed: it appears automatically when the customer
// tenant consents to or uses the corresponding Microsoft service. Customer-
// authored apps (own tenant) and managed identities (no
// appOwnerOrganizationId) fall through unmanaged.
var microsoftFirstPartyTenants = map[string]bool{
	"f8cdef31-a31e-4b4a-93e4-5f571e91255a": true, // Microsoft Services tenant — most first-party app SPs
	"72f988bf-86f1-41af-91ab-2d7cd011db47": true, // Microsoft corporate tenant
}

func isMicrosoftFirstPartySP(appOwnerTenantID string) bool {
	return microsoftFirstPartyTenants[strings.ToLower(appOwnerTenantID)]
}

func scanEntraServicePrincipals(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("servicePrincipals", []string{"id", "appId", "displayName", "servicePrincipalType", "accountEnabled", "appOwnerOrganizationId"})
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			reportEntra(st, "servicePrincipals", formatAzureError(uerr), false)
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	err := iterateGraph(ctx, g, startURL, func(sp spAttrs) bool {
		if sp.ID == "" {
			return true
		}
		name := sp.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraServicePrincipal, NativeID: sp.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(sp),
			DiscoveredBy:      scanID,
			ManagedByProvider: isMicrosoftFirstPartySP(sp.AppOwnerOrganizationID),
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
		reportEntraErr(st, "servicePrincipals", err)
	}
	flush()
	return total, inserted
}

type appAttrs struct {
	ID             string `json:"id"`
	AppID          string `json:"appId,omitempty"`
	DisplayName    string `json:"displayName"`
	SignInAudience string `json:"signInAudience,omitempty"`
}

func scanEntraApplications(ctx context.Context, g *graphClient, tenantID string, st *store.Store, scanID string) (total, inserted int) {
	startURL := g.listURL("applications", []string{"id", "appId", "displayName", "signInAudience"})
	var batch []*store.Resource
	flush := func() {
		if len(batch) == 0 {
			return
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			reportEntra(st, "applications", formatAzureError(uerr), false)
			batch = batch[:0]
			return
		}
		total += len(batch)
		inserted += n
		batch = batch[:0]
	}
	err := iterateGraph(ctx, g, startURL, func(a appAttrs) bool {
		if a.ID == "" {
			return true
		}
		name := a.DisplayName
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: tenantID,
			Region: regionGlobal,
			Type:   TypeEntraApplication, NativeID: a.ID,
			Name: &name, AttributesJSON: jsonOrEmpty(a),
			DiscoveredBy: scanID,
		})
		if len(batch) >= graphPageSize {
			flush()
		}
		return true
	})
	if err != nil {
		reportEntraErr(st, "applications", err)
	}
	flush()
	return total, inserted
}

// reportEntraErr classifies a Graph error: missing-permission paths surface
// as ScanWarning + abort the entity, hard errors surface as ScanError. A
// *graphErr is classified on its STATUS field plus the Graph error CODES in
// its body field — never on Error(), see the status paragraph below.
// Transport / parse errors fall to the hard-error branch.
//
// Classifies on the raw text and REPORTS the formatted one, because this was
// the package's only report site bypassing the formatAzureError chokepoint.
// For a *graphErr the two strings are identical unless the error is a
// credential failure — formatAzureError returns err.Error() for anything that
// is neither a credential failure nor an *azcore.ResponseError — so the change
// buys exactly one thing: a CREDENTIAL failure is redacted here like
// everywhere else. What keeps that narrow is scanBodyForAADSTS's graphErr arm,
// which requires a 401: a Graph 403 says Authorization_RequestDenied, and
// collapsing THAT would cost the customer the consent diagnostic that is
// theirs to act on. Not hypothetical: tenant scope IS reopened for Graph, and
// a token acquisition failure surfaces through this function naming disco's
// own tenant in the authority URL.
func reportEntraErr(st *store.Store, scope string, err error) {
	// TYPE first, and BEFORE formatAzureError runs. Three narrower fixes were
	// each measured insufficient, which is why the ordering is stated here
	// rather than left to read as arbitrary. Restricting the message to the host
	// does not help: url.Parse admits "Authorization_RequestDenied.evil.example"
	// as a host outright (shouldEscape permits alphanumerics and '-', '_', '.',
	// '~'), and an IPv6 ZONE admits spaces (encodeZone exempts ' '), so
	// "[fe80::1% 401]" matches " 401". Classifying by type but computing msg
	// first does not either: scanBodyForAADSTS's last arm matches "AADSTS" in
	// ANY error type, and "AADSTS700016.evil.example" is a legal host, so the
	// refusal was rewritten as "azure authentication failed (AADSTS700016); see
	// scanner logs" — host gone, refusal misfiled as a credential failure, the
	// attacker's host written to stderr, and an attacker-chosen key left in the
	// never-cleared loggedCredentialErrors map. And covering only the nextLink
	// type does not either: a transport cause carries text the server chose (Go
	// parses the Location header BEFORE consulting CheckRedirect, so an
	// unparseable one is echoed into it). All three are the same claim — see
	// neverAConsentFailure.
	if m, ok := neverAConsentFailure(err); ok {
		reportEntra(st, scope, m, false)
		return
	}
	msg := formatAzureError(err)

	// A *graphErr carries the STATUS as a typed field, so reading it out of the
	// body was gratuitous: strings.Contains(raw, " 403") matched anywhere in an
	// 8 KiB response the remote side wrote, so a 500 whose body happened to say
	// "Error 403" was filed as the routine missing-consent WARNING. The code
	// STRINGS below still come from the body, and that is correct — they are
	// Graph's own diagnosis and the classifier's intended input — but the status
	// never has to be.
	var ge *graphErr
	if errors.As(err, &ge) {
		denied := ge.status == http.StatusUnauthorized || ge.status == http.StatusForbidden ||
			strings.Contains(ge.body, "Authorization_RequestDenied") ||
			strings.Contains(ge.body, "Insufficient privileges")
		reportEntra(st, scope, msg, denied)
		return
	}

	// Everything else has no status to read, so the substring test is all there
	// is. Reached by SDK and credential errors, not by a Graph response body.
	raw := err.Error()
	if strings.Contains(raw, "Authorization_RequestDenied") ||
		strings.Contains(raw, "Insufficient privileges") ||
		strings.Contains(raw, " 401") || strings.Contains(raw, " 403") {
		reportEntra(st, scope, msg, true)
		return
	}
	reportEntra(st, scope, msg, false)
}

// reportEntra is the one place THIS PACKAGE reports a Graph-derived message.
//
// Bounding per error TYPE was the wrong shape and shipped once: the four types
// this package mints were bounded (one of them) while graphErr — which splices
// the response BODY, the largest remote-chosen string of the lot — went
// through formatAzureError's pass-through arm untouched, and hostOnly's own
// doc claimed a bound it did not have (a 20 kB host parses fine). A chokepoint
// cannot be forgotten by the next error type.
//
// What it does NOT cover, stated rather than implied: store.UpsertResources
// emits its own ScanWarning naming a colliding native_id, and for an Entra row
// that id is read verbatim out of the Graph body — so a tampered directory
// returning one id from two collections writes unbounded remote text to the
// scan record through a path in store/ that nothing here can reach. That is
// shared by every provider, so it belongs in store rather than in a fifth
// guard here; it is open.
func reportEntra(st *store.Store, scope, msg string, warn bool) {
	msg = sanitizeForScanRecord(msg)
	if warn {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:microsoft.entra", Scope: scope, Message: msg,
		})
		return
	}
	st.ReportError(store.ScanError{
		Provider: "azure", Service: "azure:microsoft.entra", Scope: scope, Message: msg,
	})
}

// neverAConsentFailure returns the message for an error this package MINTED
// against a Graph response, and reports whether err is one.
//
// All of them carry text the remote side chose — a nextLink host, a Location
// host, a transport cause that can embed a response header — so none may reach
// reportEntraErr's substring block OR formatAzureError, and none can be the
// missing-consent warning: no permission a customer could grant makes a
// refused redirect or a broken connection go away.
//
// The message comes from the MATCHED value, not from err, so a future wrapper
// around one of these cannot re-admit its own text. Any error type this
// package mints later belongs here.
func neverAConsentFailure(err error) (string, bool) {
	var foreign *foreignLinkError
	if errors.As(err, &foreign) {
		return foreign.Error(), true
	}
	var redirect *redirectRefusedError
	if errors.As(err, &redirect) {
		return redirect.Error(), true
	}
	var repeated *repeatedLinkError
	if errors.As(err, &repeated) {
		return repeated.Error(), true
	}
	var oversize *oversizePageError
	if errors.As(err, &oversize) {
		return oversize.Error(), true
	}
	var tooMany *tooManyPagesError
	if errors.As(err, &tooMany) {
		return tooMany.Error(), true
	}
	var transport *graphTransportError
	if errors.As(err, &transport) {
		return transport.Error(), true
	}
	return "", false
}

// strDeref returns the dereferenced string, "" if nil.
func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// jsonOrEmpty marshals v; returns "{}" on marshal failure (never panics —
// Resource AttributesJSON must be valid JSON for downstream rule queries).
func jsonOrEmpty(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// orgAttrs is the curated subset of a Microsoft Graph organization (tenant)
// entity. Only the friendly displayName is needed for the scan scope label.
type orgAttrs struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// tenantDisplayName fetches the tenant's friendly display name from
// Microsoft Graph's /organization endpoint (a single-element collection
// scoped to the calling tenant). Best-effort: requires directory read
// access, so on any failure the caller falls back to the tenant GUID for
// the scope label rather than surfacing a warning — the Entra scanner
// already reports Graph permission denials, and the GUID is an honest
// label on its own.
func tenantDisplayName(ctx context.Context, cred tokenIssuer) (string, error) {
	return tenantDisplayNameWithClient(ctx, newGraphClient(cred, ""))
}

// tenantDisplayNameWithClient is the testable core — tests aim it at an
// httptest-backed graphClient via the baseURL seam.
func tenantDisplayNameWithClient(ctx context.Context, g *graphClient) (string, error) {
	var page graphPage[orgAttrs]
	if err := g.get(ctx, g.listURL("organization", []string{"id", "displayName"}), &page); err != nil {
		return "", err
	}
	if len(page.Value) == 0 {
		return "", fmt.Errorf("graph: /organization returned no rows")
	}
	return page.Value[0].DisplayName, nil
}

// tenantIDFromCredScope resolves the tenant GUID from the `tid` claim of a
// token issued for the given scope. Any AAD token carries `tid`; scope only
// decides which audience must be reachable. Prefer armScope when the caller
// lacks guaranteed Graph access — every scan already obtains ARM tokens.
func tenantIDFromCredScope(ctx context.Context, cred azcore.TokenCredential, scope string) (string, error) {
	return tenantIDFromCredScopeTenant(ctx, cred, scope, "")
}

// tenantIDFromCredScopeTenant resolves the tenant GUID from a token minted for
// a NAMED directory. An empty tenantID asks for the credential's default,
// which is what every unfederated caller wants and what azidentity does with
// an unset TokenRequestOptions.TenantID.
//
// The returned value is the `tid` of the token that came back, never the
// tenantID argument echoed: the two agreeing is the only evidence available
// that the directory asked for is the directory that answered, and a caller
// that returned its own input could not tell them apart.
func tenantIDFromCredScopeTenant(ctx context.Context, cred azcore.TokenCredential, scope, tenantID string) (string, error) {
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}, TenantID: tenantID})
	if err != nil {
		return "", err
	}
	return tenantIDFromJWT(tok.Token)
}

// tenantIDFromJWT decodes the unverified JWT payload and pulls the `tid`
// claim. Signature verification is unnecessary — the token came from our
// own credential and is used by us, not consumed externally.
func tenantIDFromJWT(jwt string) (string, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("not a JWT")
	}
	payload, err := decodeJWTPayload(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Tid string `json:"tid"`
	}
	if jerr := json.Unmarshal(payload, &claims); jerr != nil {
		return "", fmt.Errorf("parse JWT claims: %w", jerr)
	}
	if claims.Tid == "" {
		return "", fmt.Errorf("no tid claim in token")
	}
	return claims.Tid, nil
}

// decodeJWTPayload decodes a JWT body segment (base64url, possibly with
// missing padding). RawURLEncoding handles missing padding; URL+padding
// edge cases fall through to URLEncoding.
func decodeJWTPayload(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
