package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/proton"
	protontypes "github.com/aws/aws-sdk-go-v2/service/proton/types"
	"github.com/icearp/disco-cli/store"
)

var (
	protonDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: proton:List")
	protonNotFound   = apiErr("ResourceNotFoundException", "gone")
	protonValidation = apiErr("ValidationException", "bad arn")
	protonThrottle   = apiErr("ThrottlingException", "slow down")
)

const (
	protonTagsOp     = "proton:ListTagsForResource"
	protonConnOp     = "ListEnvironmentAccountConnections"
	protonEnvVerOp   = "ListEnvironmentTemplateVersions"
	protonSvcVerOp   = "ListServiceTemplateVersions"
	protonOtherAcct  = "210987654321"
	protonTotalCount = 26
)

// protonKind is one account-wide listing of the fixture other than the
// connection listing: the List op, the type it stores, the ARN resource
// prefix, the label its names start with, whether its rows are tag-read, and
// page, which builds the op's output from an element's ARN and name and the
// next token.
type protonKind struct {
	op, typ, prefix, label string
	tagged                 bool
	page                   func(arn, name, next *string) any
}

func protonKinds() []protonKind {
	return []protonKind{
		{"ListEnvironmentTemplates", TypeProtonEnvironmentTemplate, "environment-template/", "envtpl", true, func(arn, name, next *string) any {
			return &proton.ListEnvironmentTemplatesOutput{Templates: []protontypes.EnvironmentTemplateSummary{{Arn: arn, Name: name}}, NextToken: next}
		}},
		{"ListServiceTemplates", TypeProtonServiceTemplate, "service-template/", "svctpl", true, func(arn, name, next *string) any {
			return &proton.ListServiceTemplatesOutput{Templates: []protontypes.ServiceTemplateSummary{{Arn: arn, Name: name}}, NextToken: next}
		}},
		{"ListEnvironments", TypeProtonEnvironment, "environment/", "env", true, func(arn, name, next *string) any {
			return &proton.ListEnvironmentsOutput{Environments: []protontypes.EnvironmentSummary{{Arn: arn, Name: name, DeploymentStatus: protontypes.DeploymentStatusSucceeded}}, NextToken: next}
		}},
		{"ListServices", TypeProtonService, "service/", "svc", true, func(arn, name, next *string) any {
			return &proton.ListServicesOutput{Services: []protontypes.ServiceSummary{{Arn: arn, Name: name, Status: protontypes.ServiceStatusActive}}, NextToken: next}
		}},
		{"ListServiceInstances", TypeProtonServiceInstance, "service/svc-tagged/service-instance/", "si", true, func(arn, name, next *string) any {
			return &proton.ListServiceInstancesOutput{ServiceInstances: []protontypes.ServiceInstanceSummary{{Arn: arn, Name: name, ServiceName: sdkaws.String("svc-tagged")}}, NextToken: next}
		}},
		{"ListComponents", TypeProtonComponent, "component/", "comp", true, func(arn, name, next *string) any {
			return &proton.ListComponentsOutput{Components: []protontypes.ComponentSummary{{Arn: arn, Name: name}}, NextToken: next}
		}},
		{"ListDeployments", TypeProtonDeployment, "deployment/", "dep", false, func(arn, name, next *string) any {
			return &proton.ListDeploymentsOutput{Deployments: []protontypes.DeploymentSummary{{Arn: arn, Id: name}}, NextToken: next}
		}},
		{"ListRepositories", TypeProtonRepository, "repository/GITHUB:", "repo", true, func(arn, name, next *string) any {
			return &proton.ListRepositoriesOutput{Repositories: []protontypes.RepositorySummary{{Arn: arn, Name: name, Provider: protontypes.RepositoryProviderGithub}}, NextToken: next}
		}},
	}
}

func protonKindOf(t *testing.T, op string) protonKind {
	t.Helper()
	for _, k := range protonKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return protonKind{}
}

// protonName is the name of the fixture's resource suffix ("tagged" or
// "bare") of kind k.
func protonName(k protonKind, suffix string) string { return k.label + "-" + suffix }

func protonARNIn(partition, region, account, resource string) string {
	return "arn:" + partition + ":proton:" + region + ":" + account + ":" + resource
}

func protonARN(k protonKind, suffix string) string {
	return protonARNIn("aws", testRegion, testAccountID, k.prefix+protonName(k, suffix))
}

// protonConnARN is the ARN of the fixture's environment account connection
// suffix in account.
func protonConnARN(account, suffix string) string {
	return protonARNIn("aws", testRegion, account, "environment-account-connection/conn-"+suffix)
}

// protonVersionOps are the per-template version listings: the op, the
// template listing it fans out over, and the version type.
var protonVersionOps = []struct{ op, templateOp, typ string }{
	{protonEnvVerOp, "ListEnvironmentTemplates", TypeProtonEnvironmentTemplateVersion},
	{protonSvcVerOp, "ListServiceTemplates", TypeProtonServiceTemplateVersion},
}

// protonVersionARN is the ARN of version ver of the fixture's template
// suffix listed by templateOp.
func protonVersionARN(t *testing.T, templateOp, suffix, ver string) string {
	return protonARN(protonKindOf(t, templateOp), suffix) + ":" + ver
}

// protonCalls records the stubbed Proton calls: how often each op ran, the
// MaxResults each was sent, the RequestedBy values and template names sent,
// and the ARNs sent to ListTagsForResource, which it answers from tags or
// errs, both keyed by ARN. Tag reads and version listings run concurrently.
type protonCalls struct {
	mu          sync.Mutex
	ops         map[string]int
	maxResults  map[string][]*int32
	requestedBy []string
	templates   map[string][]string
	tagARNs     []string
	tags        map[string]map[string]string
	errs        map[string]error
}

// newProtonCalls tags every "tagged" resource and every version 1.0 with its
// type.
func newProtonCalls(t *testing.T) *protonCalls {
	c := &protonCalls{ops: map[string]int{}, maxResults: map[string][]*int32{}, templates: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range protonKinds() {
		if k.tagged {
			c.tags[protonARN(k, "tagged")] = map[string]string{"type": k.typ}
		}
	}
	c.tags[protonConnARN(testAccountID, "tagged")] = map[string]string{"type": TypeProtonEnvironmentAccountConnection}
	for _, v := range protonVersionOps {
		for _, suffix := range []string{"tagged", "bare"} {
			c.tags[protonVersionARN(t, v.templateOp, suffix, "1.0")] = map[string]string{"type": v.typ}
		}
	}
	return c
}

func (c *protonCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
}

func (c *protonCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *protonCalls) listTags(in any) (any, error) {
	req := in.(*proton.ListTagsForResourceInput)
	arn := sv(req.ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []protontypes.Tag
	for k, v := range tags {
		list = append(list, protontypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &proton.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *protonCalls) twoPages(k protonKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := protonARN(k, suffix), protonName(k, suffix)
		return k.page(&arn, &name, next), nil
	}
}

func protonConn(arn, suffix string) protontypes.EnvironmentAccountConnectionSummary {
	return protontypes.EnvironmentAccountConnectionSummary{
		Arn: &arn, Id: sdkaws.String("id-" + suffix), EnvironmentName: sdkaws.String("conn-" + suffix),
		EnvironmentAccountId: sdkaws.String(testAccountID), ManagementAccountId: sdkaws.String(testAccountID),
		Status: protontypes.EnvironmentAccountConnectionStatusConnected,
	}
}

// connections answers the management side with "tagged" then "bare" on two
// pages, and the environment side with "tagged" again.
func (c *protonCalls) connections(in any) (any, error) {
	c.record(protonConnOp, in)
	rb := bacField[protontypes.EnvironmentAccountConnectionRequesterAccountType](in, "RequestedBy")
	c.mu.Lock()
	c.requestedBy = append(c.requestedBy, string(rb))
	c.mu.Unlock()
	if rb == protontypes.EnvironmentAccountConnectionRequesterAccountTypeEnvironmentAccount {
		return &proton.ListEnvironmentAccountConnectionsOutput{EnvironmentAccountConnections: []protontypes.EnvironmentAccountConnectionSummary{protonConn(protonConnARN(testAccountID, "tagged"), "tagged")}}, nil
	}
	suffix, next := "tagged", sdkaws.String("t2")
	if bacField[*string](in, "NextToken") != nil {
		suffix, next = "bare", nil
	}
	return &proton.ListEnvironmentAccountConnectionsOutput{EnvironmentAccountConnections: []protontypes.EnvironmentAccountConnectionSummary{protonConn(protonConnARN(testAccountID, suffix), suffix)}, NextToken: next}, nil
}

// versions answers a version listing with 1.0 on the first page and 2.0 on
// the second, for the template named, whose listing is templateOp.
func (c *protonCalls) versions(t *testing.T, op, templateOp string) func(any) (any, error) {
	k := protonKindOf(t, templateOp)
	return func(in any) (any, error) {
		c.record(op, in)
		name := sv(bacField[*string](in, "TemplateName"))
		c.mu.Lock()
		c.templates[op] = append(c.templates[op], name)
		c.mu.Unlock()
		suffix := strings.TrimPrefix(name, k.label+"-")
		major, next := "1", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			major, next = "2", nil
		}
		arn := protonVersionARN(t, templateOp, suffix, major+".0")
		if op == protonEnvVerOp {
			return &proton.ListEnvironmentTemplateVersionsOutput{TemplateVersions: []protontypes.EnvironmentTemplateVersionSummary{{Arn: &arn, TemplateName: &name, MajorVersion: &major, MinorVersion: sdkaws.String("0")}}, NextToken: next}, nil
		}
		return &proton.ListServiceTemplateVersionsOutput{TemplateVersions: []protontypes.ServiceTemplateVersionSummary{{Arn: &arn, TemplateName: &name, MajorVersion: &major, MinorVersion: sdkaws.String("0")}}, NextToken: next}, nil
	}
}

// protonHandlers answers every op the scan calls with the fixture: two
// resources of every kind, two connections, and two versions of each
// template.
func protonHandlers(t *testing.T, c *protonCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){"ListTagsForResource": c.listTags, protonConnOp: c.connections}
	for _, k := range protonKinds() {
		h[k.op] = c.twoPages(k)
	}
	for _, v := range protonVersionOps {
		h[v.op] = c.versions(t, v.op, v.templateOp)
	}
	return h
}

// protonScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func protonScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, protonDenied)
	return scanProton(ctx, acct, testRegion, st, testScanID)
}

func protonRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// protonWant is one row the full fixture stores: its type, ARN, Name and
// tags (nil for none).
type protonWant struct {
	typ, arn, name string
	tags           map[string]string
}

func protonWants(t *testing.T) []protonWant {
	var want []protonWant
	for _, k := range protonKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			w := protonWant{k.typ, protonARN(k, suffix), protonName(k, suffix), nil}
			if k.tagged && suffix == "tagged" {
				w.tags = map[string]string{"type": k.typ}
			}
			want = append(want, w)
		}
	}
	want = append(want,
		protonWant{TypeProtonEnvironmentAccountConnection, protonConnARN(testAccountID, "tagged"), "conn-tagged", map[string]string{"type": TypeProtonEnvironmentAccountConnection}},
		protonWant{TypeProtonEnvironmentAccountConnection, protonConnARN(testAccountID, "bare"), "conn-bare", nil})
	for _, v := range protonVersionOps {
		for _, suffix := range []string{"tagged", "bare"} {
			want = append(want,
				protonWant{v.typ, protonVersionARN(t, v.templateOp, suffix, "1.0"), "1.0", map[string]string{"type": v.typ}},
				protonWant{v.typ, protonVersionARN(t, v.templateOp, suffix, "2.0"), "2.0", nil})
		}
	}
	return want
}

// Every type stores both pages of its listing, named as it is listed, with
// the tags ListTagsForResource returns for its ARN, and nil for a resource
// without tags. Exactly the ARNs of every type but deployments are sent to
// the tag op. Every op is sent MaxResults 100; the connection listing is sent
// both RequestedBy values and stores the connection both return once; each
// template's versions are listed by its name. No error or warning.
func TestScanProton_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newProtonCalls(t)

	total, inserted, err := protonScan(context.Background(), st, protonHandlers(t, calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != protonTotalCount || inserted != protonTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, protonTotalCount, protonTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range protonWants(t) {
		r, ok := protonRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.arn)
			continue
		}
		if got := sv(r.Name); got != w.name {
			t.Errorf("%s %s Name = %q; want %q", w.typ, w.arn, got, w.name)
		}
		if w.tags != nil {
			assertTagsMap(t, r.TagsJSON, w.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
		if w.typ != TypeProtonDeployment {
			wantARNs = append(wantARNs, w.arn)
		}
	}
	if r, _ := protonRowOf(t, st, TypeProtonEnvironment, protonARN(protonKindOf(t, "ListEnvironments"), "tagged")); sv(r.Status) != string(protontypes.DeploymentStatusSucceeded) {
		t.Errorf("environment Status = %q; want %s", sv(r.Status), protontypes.DeploymentStatusSucceeded)
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for op, sent := range calls.maxResults {
		for _, n := range sent {
			if n == nil || *n != 100 {
				t.Errorf("%s MaxResults = %v; want 100", op, n)
			}
		}
	}
	slices.Sort(calls.requestedBy)
	if want := []string{"ENVIRONMENT_ACCOUNT", "MANAGEMENT_ACCOUNT", "MANAGEMENT_ACCOUNT"}; !slices.Equal(calls.requestedBy, want) {
		t.Errorf("RequestedBy sent = %v; want %v", calls.requestedBy, want)
	}
	for _, v := range protonVersionOps {
		k := protonKindOf(t, v.templateOp)
		got := slices.Clone(calls.templates[v.op])
		slices.Sort(got)
		want := []string{protonName(k, "bare"), protonName(k, "bare"), protonName(k, "tagged"), protonName(k, "tagged")}
		if !slices.Equal(got, want) {
			t.Errorf("%s TemplateNames = %v; want %v", v.op, got, want)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanProton_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newProtonCalls(t)
	if _, _, err := protonScan(context.Background(), st, protonHandlers(t, calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := protonScan(context.Background(), st, protonHandlers(t, calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != protonTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, protonTotalCount)
	}
}

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently; a denial keeps the row untagged and warns once
// under proton:ListTagsForResource; a throttle after retries keeps it and is
// a transient error; ValidationException keeps it and is a hard error. Each
// outcome holds for an account-wide listing's row, a template version's row
// and an own-account connection's row.
func TestScanProton_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeProtonEnvironment, protonARN(protonKindOf(t, "ListEnvironments"), "tagged")},
		{TypeProtonServiceTemplateVersion, protonVersionARN(t, "ListServiceTemplates", "bare", "1.0")},
		{TypeProtonEnvironmentAccountConnection, protonConnARN(testAccountID, "tagged")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", protonNotFound, 0, "", protonTotalCount - 1, false, 0},
				{"denied", protonDenied, 0, "", protonTotalCount, true, 1},
				{"transient", protonThrottle, outcomeTransient, "", protonTotalCount, true, 0},
				{"other error", protonValidation, outcomeError, "ValidationException", protonTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, protonTagsOp)
					calls := newProtonCalls(t)
					calls.errs[target.arn] = c.err

					total, _, err := protonScan(context.Background(), st, protonHandlers(t, calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					if total != c.wantTotal {
						t.Errorf("total = %d; want %d", total, c.wantTotal)
					}
					r, ok := protonRowOf(t, st, target.typ, target.arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", protonTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// A connection whose ARN is in another account (listed from its management
// account) is tag-read under that ARN. A denial or a not-found answer keeps
// it untagged without a warning, since this account may not be able to read
// it; any other error keeps it and is returned. Own-account connections keep
// the usual rules (TestScanProton_TagReadFailures).
func TestScanProton_ForeignConnectionTagRead(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{"denied", protonDenied, false},
		{"not found", protonNotFound, false},
		{"other error", protonValidation, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newProtonCalls(t)
			foreign := protonConnARN(protonOtherAcct, "foreign")
			calls.errs[foreign] = c.err
			handlers := protonHandlers(t, calls)
			handlers[protonConnOp] = func(in any) (any, error) {
				if bacField[protontypes.EnvironmentAccountConnectionRequesterAccountType](in, "RequestedBy") == protontypes.EnvironmentAccountConnectionRequesterAccountTypeEnvironmentAccount {
					return &proton.ListEnvironmentAccountConnectionsOutput{}, nil
				}
				return &proton.ListEnvironmentAccountConnectionsOutput{EnvironmentAccountConnections: []protontypes.EnvironmentAccountConnectionSummary{protonConn(foreign, "foreign")}}, nil
			}

			_, _, err := protonScan(context.Background(), st, handlers)
			if c.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			} else if err != nil {
				t.Fatalf("scan: %v", err)
			}
			r, ok := protonRowOf(t, st, TypeProtonEnvironmentAccountConnection, foreign)
			if !ok {
				t.Fatal("foreign connection missing")
			}
			if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if !slices.Contains(calls.tagARNs, foreign) {
				t.Errorf("foreign connection %s not sent to the tag op", foreign)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
		})
	}
}

// An element whose status enum is empty stores no Status.
func TestScanProton_EmptyStatusStoresNil(t *testing.T) {
	st := newTestStore(t)
	handlers := protonHandlers(t, newProtonCalls(t))
	k := protonKindOf(t, "ListServices")
	arn := protonARN(k, "tagged")
	handlers[k.op] = func(any) (any, error) {
		return &proton.ListServicesOutput{Services: []protontypes.ServiceSummary{{Arn: &arn, Name: sdkaws.String("svc-tagged")}}}, nil
	}

	if _, _, err := protonScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := protonRowOf(t, st, TypeProtonService, arn)
	if !ok {
		t.Fatal("service missing")
	}
	if r.Status != nil {
		t.Errorf("Status = %q; want nil", *r.Status)
	}
}

// The tag op is read page by page, each page sent the token the last
// returned and MaxResults 100, and the row gets every page's tags. A
// repeated token keeps the row without tags and is an error.
func TestScanProton_TagReadPages(t *testing.T) {
	arn := protonARN(protonKindOf(t, "ListComponents"), "tagged")
	pages := func(second *string) func(any) (any, error) {
		calls := newProtonCalls(t)
		return func(in any) (any, error) {
			req := in.(*proton.ListTagsForResourceInput)
			if sv(req.ResourceArn) != arn {
				return calls.listTags(in)
			}
			if req.MaxResults == nil || *req.MaxResults != 100 {
				return nil, errors.New("test: MaxResults not 100")
			}
			if req.NextToken == nil {
				return &proton.ListTagsForResourceOutput{Tags: []protontypes.Tag{{Key: sdkaws.String("a"), Value: sdkaws.String("1")}}, NextToken: sdkaws.String("p2")}, nil
			}
			if sv(req.NextToken) != "p2" {
				return nil, errors.New("test: wrong token")
			}
			return &proton.ListTagsForResourceOutput{Tags: []protontypes.Tag{{Key: sdkaws.String("b"), Value: sdkaws.String("2")}}, NextToken: second}, nil
		}
	}
	t.Run("every page", func(t *testing.T) {
		st := newTestStore(t)
		handlers := protonHandlers(t, newProtonCalls(t))
		handlers["ListTagsForResource"] = pages(nil)
		if _, _, err := protonScan(context.Background(), st, handlers); err != nil {
			t.Fatalf("scan: %v", err)
		}
		r, _ := protonRowOf(t, st, TypeProtonComponent, arn)
		assertTagsMap(t, r.TagsJSON, map[string]string{"a": "1", "b": "2"})
	})
	t.Run("repeated token", func(t *testing.T) {
		st := newTestStore(t)
		handlers := protonHandlers(t, newProtonCalls(t))
		handlers["ListTagsForResource"] = pages(sdkaws.String("p2"))
		_, _, err := protonScan(context.Background(), st, handlers)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		r, ok := protonRowOf(t, st, TypeProtonComponent, arn)
		if !ok || r.TagsJSON != nil {
			t.Errorf("row stored = %v, TagsJSON = %v; want stored without tags", ok, r.TagsJSON)
		}
	})
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanProton_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, protonTagsOp)
	calls := newProtonCalls(t)
	calls.errs[protonARN(protonKindOf(t, "ListRepositories"), "tagged")] = protonThrottle
	calls.errs[protonARN(protonKindOf(t, "ListServices"), "bare")] = protonValidation

	_, _, err := protonScan(context.Background(), st, protonHandlers(t, calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", protonTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and stores nothing of its
// type; every other listing still runs. A denied template listing leaves its
// versions unlisted.
func TestScanProton_DeniedListingWarnsUnderItsOp(t *testing.T) {
	ops := []struct {
		op, typ string
		lost    int
	}{{protonConnOp, TypeProtonEnvironmentAccountConnection, 2}, {protonEnvVerOp, TypeProtonEnvironmentTemplateVersion, 4}, {protonSvcVerOp, TypeProtonServiceTemplateVersion, 4}}
	for _, k := range protonKinds() {
		lost := 2
		if k.op == "ListEnvironmentTemplates" || k.op == "ListServiceTemplates" {
			lost = 6
		}
		ops = append(ops, struct {
			op, typ string
			lost    int
		}{k.op, k.typ, lost})
	}
	for _, c := range ops {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "proton:"+c.op)
			handlers := protonHandlers(t, newProtonCalls(t))
			delete(handlers, c.op)

			total, _, err := protonScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != protonTotalCount-c.lost {
				t.Errorf("total = %d; want %d", total, protonTotalCount-c.lost)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("proton:%s warnings = %v (all ops %d); want 1", c.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, c.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, got)
			}
		})
	}
}

// A version listing that fails for one template keeps the other template's
// versions: a denial warns once under the op after the write, a
// ResourceNotFoundException (the template was deleted since it was listed)
// skips the template silently, and any other error is returned.
func TestScanProton_VersionListingPerTemplate(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"denied", protonDenied, false, 1},
		{"not found", protonNotFound, false, 0},
		{"other error", protonValidation, true, 0},
	} {
		for _, v := range protonVersionOps {
			t.Run(c.name+"/"+v.op, func(t *testing.T) {
				st := newTestStore(t)
				all := countWarnings(st)
				warnings := bacWarnings(st, "proton:"+v.op)
				calls := newProtonCalls(t)
				handlers := protonHandlers(t, calls)
				bare := protonName(protonKindOf(t, v.templateOp), "bare")
				next := handlers[v.op]
				handlers[v.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, "TemplateName")) == bare {
						return nil, c.err
					}
					return next(in)
				}

				total, _, err := protonScan(context.Background(), st, handlers)
				if c.wantErr {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, "ValidationException")
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if total != protonTotalCount-2 {
					t.Errorf("total = %d; want %d", total, protonTotalCount-2)
				}
				for _, ver := range []string{"1.0", "2.0"} {
					if _, ok := protonRowOf(t, st, v.typ, protonVersionARN(t, v.templateOp, "tagged", ver)); !ok {
						t.Errorf("version %s of the tagged template missing", ver)
					}
				}
				if len(*warnings) != c.wantWarns || *all != c.wantWarns {
					t.Errorf("proton:%s warnings = %v (all ops %d); want %d", v.op, *warnings, *all, c.wantWarns)
				}
			})
		}
	}
}

// A template the tag read finds deleted is dropped and is not a parent: its
// versions are not listed.
func TestScanProton_TemplateGoneHasNoVersions(t *testing.T) {
	st := newTestStore(t)
	calls := newProtonCalls(t)
	k := protonKindOf(t, "ListEnvironmentTemplates")
	calls.errs[protonARN(k, "bare")] = protonNotFound

	if _, _, err := protonScan(context.Background(), st, protonHandlers(t, calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.templates[protonEnvVerOp]; slices.Contains(got, protonName(k, "bare")) {
		t.Errorf("%s sent the dropped template: %v", protonEnvVerOp, got)
	}
	if got := len(rowsByNativeID(t, st, TypeProtonEnvironmentTemplateVersion)); got != 2 {
		t.Errorf("environment template versions = %d; want 2", got)
	}
}

// The connection listing reads each side on its own: one side's denial or
// error keeps the other side's rows; a denial warns once, after the write,
// however many sides were denied; any other error is returned, also beside
// the other side's denial.
func TestScanProton_ConnectionSides(t *testing.T) {
	envSide := protontypes.EnvironmentAccountConnectionRequesterAccountTypeEnvironmentAccount
	for _, c := range []struct {
		name      string
		fail      func(rb protontypes.EnvironmentAccountConnectionRequesterAccountType) error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"management side denied", func(rb protontypes.EnvironmentAccountConnectionRequesterAccountType) error {
			if rb != envSide {
				return protonDenied
			}
			return nil
		}, false, 1, 1},
		{"both sides denied", func(protontypes.EnvironmentAccountConnectionRequesterAccountType) error { return protonDenied }, false, 0, 1},
		{"environment side fails", func(rb protontypes.EnvironmentAccountConnectionRequesterAccountType) error {
			if rb == envSide {
				return protonValidation
			}
			return nil
		}, true, 2, 0},
		{"one side denied, the other fails", func(rb protontypes.EnvironmentAccountConnectionRequesterAccountType) error {
			if rb == envSide {
				return protonValidation
			}
			return protonDenied
		}, true, 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "proton:"+protonConnOp)
			calls := newProtonCalls(t)
			handlers := protonHandlers(t, calls)
			handlers[protonConnOp] = func(in any) (any, error) {
				if err := c.fail(bacField[protontypes.EnvironmentAccountConnectionRequesterAccountType](in, "RequestedBy")); err != nil {
					return nil, err
				}
				return calls.connections(in)
			}

			_, _, err := protonScan(context.Background(), st, handlers)
			if c.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := len(rowsByNativeID(t, st, TypeProtonEnvironmentAccountConnection)); got != c.wantRows {
				t.Errorf("connections = %d; want %d", got, c.wantRows)
			}
			if len(*warnings) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("proton:%s warnings = %v (all ops %d); want %d", protonConnOp, *warnings, *all, c.wantWarns)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanProton_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeProtonEnvironmentAccountConnection)
	calls := newProtonCalls(t)
	calls.errs[protonConnARN(testAccountID, "tagged")] = protonDenied
	handlers := protonHandlers(t, calls)
	conns := handlers[protonConnOp]
	handlers[protonConnOp] = func(in any) (any, error) {
		if bacField[protontypes.EnvironmentAccountConnectionRequesterAccountType](in, "RequestedBy") == protontypes.EnvironmentAccountConnectionRequesterAccountTypeEnvironmentAccount {
			return nil, protonDenied
		}
		return conns(in)
	}

	_, _, err := protonScan(context.Background(), st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListEnvironmentTemplates"); n != 0 {
		t.Errorf("ListEnvironmentTemplates called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanProton_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := protonHandlers(t, newProtonCalls(t))
	handlers["ListEnvironments"] = failOnCall(2, protonValidation, handlers["ListEnvironments"])

	total, _, err := protonScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != protonTotalCount-1 {
		t.Errorf("total = %d; want %d", total, protonTotalCount-1)
	}
	k := protonKindOf(t, "ListEnvironments")
	if _, ok := protonRowOf(t, st, k.typ, protonARN(k, "tagged")); !ok {
		t.Error("environment from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeProtonServiceTemplateVersion)); got != 4 {
		t.Errorf("service template versions stored = %d; want 4", got)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanProton_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := protonHandlers(t, newProtonCalls(t))
	k := protonKindOf(t, "ListDeployments")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, name := protonARN(k, suffix), protonName(k, suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}

	total, _, err := protonScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != protonTotalCount {
		t.Errorf("total = %d; want %d", total, protonTotalCount)
	}
	for _, suffix := range []string{"tagged", "bare"} {
		if _, ok := protonRowOf(t, st, k.typ, protonARN(k, suffix)); !ok {
			t.Errorf("deployment %s from the pages read missing", suffix)
		}
	}
}

// An element without an ARN is warned about under its listing, not stored;
// one without a name is stored, named by its ARN.
func TestScanProton_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	warns := bacWarnings(st, "proton:ListComponents")
	handlers := protonHandlers(t, newProtonCalls(t))
	unnamed := protonARNIn("aws", testRegion, testAccountID, "component/unnamed")
	handlers["ListComponents"] = func(any) (any, error) {
		return &proton.ListComponentsOutput{Components: []protontypes.ComponentSummary{
			{Name: sdkaws.String("no-arn")},
			{Arn: sdkaws.String(unnamed)},
		}}, nil
	}

	total, _, err := protonScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != protonTotalCount-1 {
		t.Errorf("total = %d; want %d", total, protonTotalCount-1)
	}
	if r, ok := protonRowOf(t, st, TypeProtonComponent, unnamed); !ok || sv(r.Name) != unnamed {
		t.Errorf("unnamed component stored = %v, Name = %q; want stored, named by its ARN", ok, sv(r.Name))
	}
	if len(*warns) != 1 || *all != 1 {
		t.Errorf("proton:ListComponents warnings = %v (all ops %d); want 1", *warns, *all)
	}
}

// Rows of an account in another partition are sent to the tag op under the
// ARNs their listings return.
func TestScanProton_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-us-gov")
	region := acct.Regions[0]
	st := newTestStore(t)
	calls := newProtonCalls(t)
	gov := protonARNIn("aws-us-gov", region, testAccountID, "environment/env-gov")
	calls.tags[gov] = map[string]string{"k": "v"}
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"ListEnvironments": func(any) (any, error) {
			return &proton.ListEnvironmentsOutput{Environments: []protontypes.EnvironmentSummary{{Arn: &gov, Name: sdkaws.String("env-gov")}}}, nil
		},
	}, protonDenied)

	if _, _, err := scanProton(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(calls.tagARNs, []string{gov}) {
		t.Errorf("ListTagsForResource ARNs = %v; want [%s]", calls.tagARNs, gov)
	}
	r, ok := protonRowOf(t, st, TypeProtonEnvironment, gov)
	if !ok {
		t.Fatal("gov environment missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanProton_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newProtonCalls(t)
	handlers := protonHandlers(t, calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	templates := handlers["ListEnvironmentTemplates"]
	handlers["ListEnvironmentTemplates"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return templates(in)
	}

	_, _, err := protonScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	for _, op := range []string{"ListServiceTemplates", "ListRepositories", protonEnvVerOp} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanProton_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "proton.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := protonHandlers(t, newProtonCalls(t))
		handlers["ListRepositories"] = func(any) (any, error) { return nil, nx }
		total, _, err := protonScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != protonTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, protonTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newProtonCalls(t)
		calls.errs[protonConnARN(testAccountID, "bare")] = nx
		_, _, err := protonScan(context.Background(), st, protonHandlers(t, calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanProton(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}
