package aws

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/networkmanager"
	nmtypes "github.com/aws/aws-sdk-go-v2/service/networkmanager/types"
	"github.com/icearp/disco-cli/store"
)

const (
	nmTestRegion = "us-west-2"
	nmTestGN1    = "arn:aws:networkmanager::123456789012:global-network/gn-1"
	nmTestGN2    = "arn:aws:networkmanager::123456789012:global-network/gn-2"
	nmTestCore1  = "arn:aws:networkmanager::123456789012:core-network/core-1"
	nmTestCore2  = "arn:aws:networkmanager::123456789012:core-network/core-2"
)

var nmTestTags = []nmtypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}}

// nmTagsFor gives the elements of the first parent (gn-1, core-1) tags and
// those of the second none.
func nmTagsFor(parent string) []nmtypes.Tag {
	if parent == "gn-1" || parent == "core-1" {
		return nmTestTags
	}
	return nil
}

// nmAllHandlers serves two global networks (gn-1 tagged, gn-2 not), two core
// networks (core-1 tagged, core-2 not) and, under each, one element per child
// listing that is tagged when its parent is. Account-wide listings serve one
// tagged and one untagged element. Every listing is served as two pages
// (nmTwoPages). A per-parent call for an id the scanner did not list fails
// the test.
func nmAllHandlers(t *testing.T) map[string]func(in any) (any, error) {
	h := nmOnePageHandlers(t)
	for op, f := range h {
		h[op] = nmTwoPages(t, op, f)
	}
	return h
}

// nmTwoPages makes serve the first of two pages: a call without NextToken
// gets serve's output with NextToken "t1", a call with "t1" gets an empty
// last page. A token not forwarded therefore repeats page one and ends the
// listing with errRepeatedNextToken. Every call must ask for the documented
// page cap of 500.
func nmTwoPages(t *testing.T, op string, serve func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := reflect.ValueOf(in).Elem()
		if mr := req.FieldByName("MaxResults").Interface().(*int32); mr == nil || *mr != 500 {
			t.Errorf("%s MaxResults = %v, want 500", op, mr)
		}
		switch token := req.FieldByName("NextToken").Interface().(*string); sv(token) {
		case "":
			out, err := serve(in)
			if err == nil {
				reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(ptrStr("t1")))
			}
			return out, err
		case "t1":
			out, err := serve(in)
			if err != nil {
				return nil, err
			}
			return reflect.New(reflect.TypeOf(out).Elem()).Interface(), nil
		default:
			t.Errorf("%s NextToken = %q, want t1", op, sv(token))
			return nil, errors.New("bad token")
		}
	}
}

// nmOnePageHandlers is nmAllHandlers' content, one page per listing.
func nmOnePageHandlers(t *testing.T) map[string]func(in any) (any, error) {
	gid := func(id *string) string {
		g := sv(id)
		if g != "gn-1" && g != "gn-2" {
			t.Errorf("GlobalNetworkId = %q, want gn-1 or gn-2", g)
		}
		return g
	}
	return map[string]func(in any) (any, error){
		"DescribeGlobalNetworks": func(any) (any, error) {
			return &networkmanager.DescribeGlobalNetworksOutput{GlobalNetworks: []nmtypes.GlobalNetwork{
				{GlobalNetworkArn: ptrStr(nmTestGN1), GlobalNetworkId: ptrStr("gn-1"), Tags: nmTestTags},
				{GlobalNetworkArn: ptrStr(nmTestGN2), GlobalNetworkId: ptrStr("gn-2")},
			}}, nil
		},
		"ListCoreNetworks": func(any) (any, error) {
			return &networkmanager.ListCoreNetworksOutput{CoreNetworks: []nmtypes.CoreNetworkSummary{
				{CoreNetworkArn: ptrStr(nmTestCore1), CoreNetworkId: ptrStr("core-1"), Tags: nmTestTags},
				{CoreNetworkArn: ptrStr(nmTestCore2), CoreNetworkId: ptrStr("core-2")},
			}}, nil
		},
		"GetSites": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetSitesInput).GlobalNetworkId)
			return &networkmanager.GetSitesOutput{Sites: []nmtypes.Site{
				{SiteArn: ptrStr("arn:site/" + g), SiteId: ptrStr("site-" + g), Tags: nmTagsFor(g)},
			}}, nil
		},
		"GetDevices": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetDevicesInput).GlobalNetworkId)
			return &networkmanager.GetDevicesOutput{Devices: []nmtypes.Device{
				{DeviceArn: ptrStr("arn:device/" + g), DeviceId: ptrStr("dev-" + g), Tags: nmTagsFor(g)},
			}}, nil
		},
		"GetLinks": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetLinksInput).GlobalNetworkId)
			return &networkmanager.GetLinksOutput{Links: []nmtypes.Link{
				{LinkArn: ptrStr("arn:link/" + g), LinkId: ptrStr("link-" + g), Tags: nmTagsFor(g)},
			}}, nil
		},
		"GetConnections": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetConnectionsInput).GlobalNetworkId)
			return &networkmanager.GetConnectionsOutput{Connections: []nmtypes.Connection{
				{ConnectionArn: ptrStr("arn:connection/" + g), ConnectionId: ptrStr("conn-" + g), Tags: nmTagsFor(g)},
			}}, nil
		},
		"GetLinkAssociations": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetLinkAssociationsInput).GlobalNetworkId)
			return &networkmanager.GetLinkAssociationsOutput{LinkAssociations: []nmtypes.LinkAssociation{
				{DeviceId: ptrStr("dev-" + g), LinkId: ptrStr("link-" + g)},
			}}, nil
		},
		"GetCustomerGatewayAssociations": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetCustomerGatewayAssociationsInput).GlobalNetworkId)
			return &networkmanager.GetCustomerGatewayAssociationsOutput{CustomerGatewayAssociations: []nmtypes.CustomerGatewayAssociation{
				{CustomerGatewayArn: ptrStr("arn:cgw/" + g)},
			}}, nil
		},
		"GetTransitGatewayRegistrations": func(in any) (any, error) {
			g := gid(in.(*networkmanager.GetTransitGatewayRegistrationsInput).GlobalNetworkId)
			return &networkmanager.GetTransitGatewayRegistrationsOutput{TransitGatewayRegistrations: []nmtypes.TransitGatewayRegistration{
				{TransitGatewayArn: ptrStr("arn:tgw/" + g)},
			}}, nil
		},
		"ListCoreNetworkPrefixListAssociations": func(in any) (any, error) {
			c := sv(in.(*networkmanager.ListCoreNetworkPrefixListAssociationsInput).CoreNetworkId)
			if c != "core-1" && c != "core-2" {
				t.Errorf("CoreNetworkId = %q, want core-1 or core-2", c)
			}
			return &networkmanager.ListCoreNetworkPrefixListAssociationsOutput{PrefixListAssociations: []nmtypes.PrefixListAssociation{
				{PrefixListArn: ptrStr("arn:pl/" + c)},
			}}, nil
		},
		"ListAttachments": func(any) (any, error) {
			return &networkmanager.ListAttachmentsOutput{Attachments: []nmtypes.Attachment{
				{AttachmentId: ptrStr("att-connect"), AttachmentType: nmtypes.AttachmentTypeConnect, Tags: nmTestTags},
				{AttachmentId: ptrStr("att-vpc"), AttachmentType: nmtypes.AttachmentTypeVpc, Tags: nmTestTags},
				{AttachmentId: ptrStr("att-vpc-untagged"), AttachmentType: nmtypes.AttachmentTypeVpc},
				{AttachmentId: ptrStr("att-vpn"), AttachmentType: nmtypes.AttachmentTypeSiteToSiteVpn, Tags: nmTestTags},
				{AttachmentId: ptrStr("att-dx"), AttachmentType: nmtypes.AttachmentTypeDirectConnectGateway, Tags: nmTestTags},
				{AttachmentId: ptrStr("att-rtb"), AttachmentType: nmtypes.AttachmentTypeTransitGatewayRouteTable, Tags: nmTestTags},
			}}, nil
		},
		"ListPeerings": func(any) (any, error) {
			return &networkmanager.ListPeeringsOutput{Peerings: []nmtypes.Peering{
				{PeeringId: ptrStr("peer-1"), PeeringType: nmtypes.PeeringTypeTransitGateway, Tags: nmTestTags},
				{PeeringId: ptrStr("peer-2"), PeeringType: nmtypes.PeeringTypeTransitGateway},
			}}, nil
		},
		"ListConnectPeers": func(any) (any, error) {
			return &networkmanager.ListConnectPeersOutput{ConnectPeers: []nmtypes.ConnectPeerSummary{
				{ConnectPeerId: ptrStr("cp-1"), Tags: nmTestTags},
				{ConnectPeerId: ptrStr("cp-2")},
			}}, nil
		},
	}
}

// nmTestAttachment is the NativeID the scanner builds for attachment id.
func nmTestAttachment(id string) string {
	return "arn:aws:networkmanager::123456789012:attachment/" + id
}

func nmScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	client := networkmanager.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanNMAll(context.Background(), client, newTestAccount(testAccountID), nmTestRegion, st, testScanID)
	return total, err
}

// TestScanNMAll_StoresTags pins, for every type whose listed element carries
// Tags, that the stored row's TagsJSON is that element's tags and that an
// untagged element stores none; the types whose element has no Tags store
// none although their parent is tagged.
func TestScanNMAll_StoresTags(t *testing.T) {
	st := newTestStore(t)
	total, err := nmScanAll(t, st, nmAllHandlers(t))
	if err != nil {
		t.Fatalf("scanNMAll: %v", err)
	}
	if total != 30 {
		t.Errorf("total = %d, want 30", total)
	}
	acct := testAccountID
	cases := []struct {
		typ, tagged, untagged string
		name                  string // of the first row named
	}{
		{TypeNetworkManagerGlobalNetwork, nmTestGN1, nmTestGN2, "gn-1"},
		{TypeNetworkManagerCoreNetwork, nmTestCore1, nmTestCore2, "core-1"},
		{TypeNetworkManagerSite, "arn:site/gn-1", "arn:site/gn-2", "site-gn-1"},
		{TypeNetworkManagerDevice, "arn:device/gn-1", "arn:device/gn-2", "dev-gn-1"},
		{TypeNetworkManagerLink, "arn:link/gn-1", "arn:link/gn-2", "link-gn-1"},
		{TypeNetworkManagerConnection, "arn:connection/gn-1", "arn:connection/gn-2", "conn-gn-1"},
		{TypeNetworkManagerConnectAttachment, nmTestAttachment("att-connect"), "", "att-connect"},
		{TypeNetworkManagerVpcAttachment, nmTestAttachment("att-vpc"), nmTestAttachment("att-vpc-untagged"), "att-vpc"},
		{TypeNetworkManagerSiteToSiteVpnAttachment, nmTestAttachment("att-vpn"), "", "att-vpn"},
		{TypeNetworkManagerDirectConnectGatewayAttachment, nmTestAttachment("att-dx"), "", "att-dx"},
		{TypeNetworkManagerTransitGatewayRouteTableAttachment, nmTestAttachment("att-rtb"), "", "att-rtb"},
		{TypeNetworkManagerTransitGatewayPeering, "arn:aws:networkmanager::" + acct + ":peering/peer-1", "arn:aws:networkmanager::" + acct + ":peering/peer-2", "peer-1"},
		{TypeNetworkManagerConnectPeer, "arn:aws:networkmanager::" + acct + ":connect-peer/cp-1", "arn:aws:networkmanager::" + acct + ":connect-peer/cp-2", "cp-1"},
		{TypeNetworkManagerLinkAssociation, "", "arn:aws:networkmanager::" + acct + ":global-network/gn-1/link-association/dev-gn-1/link-gn-1", "dev-gn-1/link-gn-1"},
		{TypeNetworkManagerCustomerGatewayAssociation, "", "arn:aws:networkmanager::" + acct + ":global-network/gn-1/cgw-association/arn:cgw/gn-1", "arn:cgw/gn-1"},
		{TypeNetworkManagerTransitGatewayRegistration, "", "arn:aws:networkmanager::" + acct + ":global-network/gn-1/tgw-registration/arn:tgw/gn-1", "arn:tgw/gn-1"},
		{TypeNetworkManagerCoreNetworkPrefixListAssociation, "", "arn:aws:networkmanager::" + acct + ":core-network/core-1/prefix-list-association/arn:pl/core-1", "arn:pl/core-1"},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			rows := rowsByNativeID(t, st, c.typ)
			named := cmp.Or(c.tagged, c.untagged)
			if got := sv(rows[named].Name); got != c.name {
				t.Errorf("%s Name = %q, want %q", named, got, c.name)
			}
			if c.tagged != "" {
				r, ok := rows[c.tagged]
				if !ok {
					t.Fatalf("%s not stored; got %d rows", c.tagged, len(rows))
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"env": "prod"})
			}
			if c.untagged != "" {
				r, ok := rows[c.untagged]
				if !ok {
					t.Fatalf("%s not stored; got %d rows", c.untagged, len(rows))
				}
				if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s, want nil", c.untagged, *r.TagsJSON)
				}
			}
		})
	}
}

// nmSitesPage is one GetSites page: sites with these ARNs ("" for none) and
// next as its NextToken, or err instead.
type nmSitesPage struct {
	arns []string
	next string
	err  error
}

// nmSitesHandler serves GetSites per global network from pages, page i of a
// parent answering NextToken "t<i>" (nmPageIndex). called, when set, records
// each parent listed.
func nmSitesHandler(t *testing.T, pages map[string][]nmSitesPage, called *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"GetSites": func(in any) (any, error) {
			req := in.(*networkmanager.GetSitesInput)
			g, token := sv(req.GlobalNetworkId), sv(req.NextToken)
			if called != nil {
				called.add(g)
			}
			idx, err := nmPageIndex(t, "GetSites "+g, token, len(pages[g]))
			if err != nil {
				return nil, err
			}
			p := pages[g][idx]
			if p.err != nil {
				return nil, p.err
			}
			out := &networkmanager.GetSitesOutput{}
			for _, a := range p.arns {
				site := nmtypes.Site{SiteId: ptrStr("site")}
				if a != "" {
					site.SiteArn = ptrStr(a)
				}
				out.Sites = append(out.Sites, site)
			}
			if p.next != "" {
				out.NextToken = ptrStr(p.next)
			}
			return out, nil
		},
	}
}

// nmPageIndex maps the NextToken a stub was sent to the page it answers: ""
// is page 0 and "t<i>" page i. A token naming no page of the n served fails
// the test, so a token the scanner did not forward is caught.
func nmPageIndex(t *testing.T, op, token string, n int) (int, error) {
	idx := 0
	if token != "" {
		i, err := strconv.Atoi(strings.TrimPrefix(token, "t"))
		if err != nil || !strings.HasPrefix(token, "t") {
			t.Errorf("%s NextToken = %q, want t<n>", op, token)
			return 0, errors.New("bad token")
		}
		idx = i
	}
	if idx >= n {
		t.Errorf("%s called with NextToken %q past its %d pages", op, token, n)
		return 0, errors.New("no such page")
	}
	return idx, nil
}

func nmScanSites(t *testing.T, st *store.Store, h map[string]func(in any) (any, error), gids ...string) (int, error) {
	t.Helper()
	client := networkmanager.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanNMSites(context.Background(), client, newTestAccount(testAccountID), nmTestRegion, st, testScanID, gids)
	return total, err
}

// TestScanNMSites_ParentOutcomes pins the per-parent error handling shared by
// every per-global-network and per-core-network listing: what each kind of
// failure on gn-1 does to the result, and that gn-2's rows are stored anyway.
func TestScanNMSites_ParentOutcomes(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	notFound := apiErr("ResourceNotFoundException", "gone")
	denied := apiErr("AccessDeniedException", "not authorized to perform: networkmanager:GetSites")
	sibling := []nmSitesPage{{arns: []string{"arn:site/b"}}}
	for _, c := range []struct {
		name      string
		gn1       []nmSitesPage
		wantErr   error
		wantARNs  []string
		wantWarns int
	}{
		{"two pages, token forwarded", []nmSitesPage{{arns: []string{"arn:site/a1"}, next: "t1"}, {arns: []string{"arn:site/a2"}}}, nil, []string{"arn:site/a1", "arn:site/a2", "arn:site/b"}, 0},
		{"hard error keeps sibling", []nmSitesPage{{err: bad}}, bad, []string{"arn:site/b"}, 0},
		{"later page fails keeps rows read", []nmSitesPage{{arns: []string{"arn:site/a1"}, next: "t1"}, {err: bad}}, bad, []string{"arn:site/a1", "arn:site/b"}, 0},
		{"repeated token keeps rows read", []nmSitesPage{{arns: []string{"arn:site/a1"}, next: "t1"}, {arns: []string{"arn:site/a2"}, next: "t1"}}, errRepeatedNextToken, []string{"arn:site/a1", "arn:site/a2", "arn:site/b"}, 0},
		{"not found before any row is silent", []nmSitesPage{{err: notFound}}, nil, []string{"arn:site/b"}, 0},
		{"not found after a page keeps rows and warns", []nmSitesPage{{arns: []string{"arn:site/a1"}, next: "t1"}, {err: notFound}}, nil, []string{"arn:site/a1", "arn:site/b"}, 1},
		{"denied warns", []nmSitesPage{{err: denied}}, nil, []string{"arn:site/b"}, 1},
		{"denied after a page keeps rows and warns", []nmSitesPage{{arns: []string{"arn:site/a1"}, next: "t1"}, {err: denied}}, nil, []string{"arn:site/a1", "arn:site/b"}, 1},
		{"empty ARN warns", []nmSitesPage{{arns: []string{"", "arn:site/a1"}}}, nil, []string{"arn:site/a1", "arn:site/b"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, nmGetSitesOp)
			h := nmSitesHandler(t, map[string][]nmSitesPage{"gn-1": c.gn1, "gn-2": sibling}, nil)
			total, err := nmScanSites(t, st, h, "gn-1", "gn-2")
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.wantARNs) {
				t.Errorf("total = %d, want %d", total, len(c.wantARNs))
			}
			rows := rowsByNativeID(t, st, TypeNetworkManagerSite)
			if len(rows) != len(c.wantARNs) {
				t.Errorf("stored %d sites, want %v", len(rows), c.wantARNs)
			}
			for _, a := range c.wantARNs {
				if _, ok := rows[a]; !ok {
					t.Errorf("site %s not stored", a)
				}
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

// TestScanNMSites_DeniedParentsWarnOnce pins one denial warning for the op
// however many parents were denied, scoped to the account and home region
// like the op's other warnings.
func TestScanNMSites_DeniedParentsWarnOnce(t *testing.T) {
	denied := apiErr("AccessDeniedException", "not authorized to perform: networkmanager:GetSites")
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == nmGetSitesOp {
			scopes = append(scopes, w.Scope)
		}
	}
	h := nmSitesHandler(t, map[string][]nmSitesPage{"gn-1": {{err: denied}}, "gn-2": {{err: denied}}}, nil)
	if _, err := nmScanSites(t, st, h, "gn-1", "gn-2"); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := testAccountID + "/" + nmTestRegion
	if len(scopes) != 1 || scopes[0] != want {
		t.Errorf("warning scopes = %v, want [%s]", scopes, want)
	}
}

// TestScanNMSites_WriteFailsWarnsNothing pins that a failed write returns
// ErrStoreWrite and reports no warning, since none could say the rows read
// were stored.
func TestScanNMSites_WriteFailsWarnsNothing(t *testing.T) {
	denied := apiErr("AccessDeniedException", "not authorized to perform: networkmanager:GetSites")
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, nmGetSitesOp)
	h := nmSitesHandler(t, map[string][]nmSitesPage{"gn-1": {{err: denied}}, "gn-2": {{arns: []string{"", "arn:site/b"}}}}, nil)
	serve := h["GetSites"]
	h["GetSites"] = func(in any) (any, error) {
		closeStore()
		return serve(in)
	}
	if _, err := nmScanSites(t, st, h, "gn-1", "gn-2"); !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// TestScanNMSites_CancelStopsParents pins that a cancelled scan lists no
// further parent.
func TestScanNMSites_CancelStopsParents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called parentCalls
	h := nmSitesHandler(t, map[string][]nmSitesPage{"gn-1": {{arns: []string{"arn:site/a1"}}}, "gn-2": {{arns: []string{"arn:site/b"}}}}, &called)
	serve := h["GetSites"]
	h["GetSites"] = func(in any) (any, error) {
		cancel()
		return serve(in)
	}
	st := newTestStore(t)
	client := networkmanager.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	_, _, err := scanNMSites(ctx, client, newTestAccount(testAccountID), nmTestRegion, st, testScanID, []string{"gn-1", "gn-2"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := called.distinct(); len(got) != 1 || got[0] != "gn-1" {
		t.Errorf("parents listed = %v, want [gn-1]", got)
	}
}

// nmAttachmentsHandler serves ListAttachments from pages, page i answering
// NextToken "t<i>" (nmPageIndex).
func nmAttachmentsHandler(t *testing.T, pages []func() (*networkmanager.ListAttachmentsOutput, error)) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListAttachments": func(in any) (any, error) {
			token := sv(in.(*networkmanager.ListAttachmentsInput).NextToken)
			idx, err := nmPageIndex(t, "ListAttachments", token, len(pages))
			if err != nil {
				return nil, err
			}
			return pages[idx]()
		},
	}
}

func nmVPCPage(next string, ids ...string) func() (*networkmanager.ListAttachmentsOutput, error) {
	return func() (*networkmanager.ListAttachmentsOutput, error) {
		out := &networkmanager.ListAttachmentsOutput{}
		for _, id := range ids {
			out.Attachments = append(out.Attachments, nmtypes.Attachment{AttachmentId: ptrStr(id), AttachmentType: nmtypes.AttachmentTypeVpc})
		}
		if next != "" {
			out.NextToken = ptrStr(next)
		}
		return out, nil
	}
}

func nmFailPage(err error) func() (*networkmanager.ListAttachmentsOutput, error) {
	return func() (*networkmanager.ListAttachmentsOutput, error) { return nil, err }
}

// TestScanNMAttachments_ListOutcomes pins the error handling shared by every
// account-wide listing: rows read before a failure are kept, a denial warns
// instead of failing, and an unstorable item warns once per reason.
func TestScanNMAttachments_ListOutcomes(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	denied := apiErr("AccessDeniedException", "not authorized to perform: networkmanager:ListAttachments")
	unstorable := func() (*networkmanager.ListAttachmentsOutput, error) {
		return &networkmanager.ListAttachmentsOutput{Attachments: []nmtypes.Attachment{
			{AttachmentType: nmtypes.AttachmentTypeVpc},
			{AttachmentId: ptrStr("att-new"), AttachmentType: "NEW_KIND"},
			{AttachmentId: ptrStr("att-newer"), AttachmentType: "NEW_KIND"},
			{AttachmentId: ptrStr("att-1"), AttachmentType: nmtypes.AttachmentTypeVpc},
		}}, nil
	}
	for _, c := range []struct {
		name      string
		pages     []func() (*networkmanager.ListAttachmentsOutput, error)
		wantErr   error
		wantIDs   []string
		wantWarns int
	}{
		{"two pages, token forwarded", []func() (*networkmanager.ListAttachmentsOutput, error){nmVPCPage("t1", "att-1"), nmVPCPage("", "att-2")}, nil, []string{"att-1", "att-2"}, 0},
		{"later page fails keeps rows read", []func() (*networkmanager.ListAttachmentsOutput, error){nmVPCPage("t1", "att-1"), nmFailPage(bad)}, bad, []string{"att-1"}, 0},
		{"repeated token keeps rows read", []func() (*networkmanager.ListAttachmentsOutput, error){nmVPCPage("t1", "att-1"), nmVPCPage("t1", "att-2")}, errRepeatedNextToken, []string{"att-1", "att-2"}, 0},
		{"denied warns", []func() (*networkmanager.ListAttachmentsOutput, error){nmFailPage(denied)}, nil, nil, 1},
		{"denied after a page keeps rows and warns", []func() (*networkmanager.ListAttachmentsOutput, error){nmVPCPage("t1", "att-1"), nmFailPage(denied)}, nil, []string{"att-1"}, 1},
		{"empty id and unmapped type warn once each", []func() (*networkmanager.ListAttachmentsOutput, error){unstorable}, nil, []string{"att-1"}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, nmListAttachmentsOp)
			client := networkmanager.NewFromConfig(opStubConfig(nmAttachmentsHandler(t, c.pages), errors.New("unexpected op")))
			total, _, err := scanNMAttachments(context.Background(), client, newTestAccount(testAccountID), nmTestRegion, st, testScanID)
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.wantIDs) {
				t.Errorf("total = %d, want %d", total, len(c.wantIDs))
			}
			rows := rowsByNativeID(t, st, TypeNetworkManagerVpcAttachment)
			if len(rows) != len(c.wantIDs) {
				t.Errorf("stored %d attachments, want %v", len(rows), c.wantIDs)
			}
			for _, id := range c.wantIDs {
				if _, ok := rows[nmTestAttachment(id)]; !ok {
					t.Errorf("attachment %s not stored", id)
				}
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

// TestScanNMAttachments_WriteFailsWarnsNothing pins that a failed write
// returns ErrStoreWrite and reports no warning.
func TestScanNMAttachments_WriteFailsWarnsNothing(t *testing.T) {
	denied := apiErr("AccessDeniedException", "not authorized to perform: networkmanager:ListAttachments")
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, nmListAttachmentsOp)
	first := nmVPCPage("t1", "att-1")
	pages := []func() (*networkmanager.ListAttachmentsOutput, error){
		func() (*networkmanager.ListAttachmentsOutput, error) {
			closeStore()
			return first()
		},
		nmFailPage(denied),
	}
	client := networkmanager.NewFromConfig(opStubConfig(nmAttachmentsHandler(t, pages), errors.New("unexpected op")))
	_, _, err := scanNMAttachments(context.Background(), client, newTestAccount(testAccountID), nmTestRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// TestScanNMPeerings_UnmappedTypeWarns pins that a PeeringType other than
// TRANSIT_GATEWAY is warned about, not dropped silently.
func TestScanNMPeerings_UnmappedTypeWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, nmListPeeringsOp)
	h := map[string]func(in any) (any, error){
		"ListPeerings": func(any) (any, error) {
			return &networkmanager.ListPeeringsOutput{Peerings: []nmtypes.Peering{
				{PeeringId: ptrStr("peer-new"), PeeringType: "NEW_KIND"},
				{PeeringId: ptrStr("peer-1"), PeeringType: nmtypes.PeeringTypeTransitGateway},
			}}, nil
		},
	}
	client := networkmanager.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanNMPeerings(context.Background(), client, newTestAccount(testAccountID), nmTestRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 1 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d; want 1, 1", total, *warns)
	}
}

// TestScanNMAll_ParentWithoutIDStoredAndWarned pins that a global or core
// network listed with an ARN but no id is stored with a warning, and that no
// child listing is called with an empty id; one listed with no ARN is warned
// about and not stored.
func TestScanNMAll_ParentWithoutIDStoredAndWarned(t *testing.T) {
	h := nmAllHandlers(t)
	h["DescribeGlobalNetworks"] = func(any) (any, error) {
		return &networkmanager.DescribeGlobalNetworksOutput{GlobalNetworks: []nmtypes.GlobalNetwork{
			{GlobalNetworkArn: ptrStr(nmTestGN1)},
			{GlobalNetworkId: ptrStr("gn-2")},
		}}, nil
	}
	h["ListCoreNetworks"] = func(any) (any, error) {
		return &networkmanager.ListCoreNetworksOutput{CoreNetworks: []nmtypes.CoreNetworkSummary{
			{CoreNetworkArn: ptrStr(nmTestCore1)},
			{CoreNetworkId: ptrStr("core-2")},
		}}, nil
	}
	for _, op := range []string{"GetSites", "GetDevices", "GetLinks", "GetConnections", "GetLinkAssociations", "GetCustomerGatewayAssociations", "GetTransitGatewayRegistrations", "ListCoreNetworkPrefixListAssociations"} {
		h[op] = func(any) (any, error) {
			t.Errorf("%s called; want no child listing for a parent without an id", op)
			return nil, errors.New("unexpected")
		}
	}
	st := newTestStore(t)
	gnWarns := countOpWarnings(st, nmDescribeGlobalNetworksOp)
	coreWarns := countOpWarnings(st, nmListCoreNetworksOp)
	if _, err := nmScanAll(t, st, h); err != nil {
		t.Fatalf("scanNMAll: %v", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeNetworkManagerGlobalNetwork)[nmTestGN1]; !ok {
		t.Errorf("global network %s not stored", nmTestGN1)
	}
	if _, ok := rowsByNativeID(t, st, TypeNetworkManagerCoreNetwork)[nmTestCore1]; !ok {
		t.Errorf("core network %s not stored", nmTestCore1)
	}
	if *gnWarns != 2 || *coreWarns != 2 {
		t.Errorf("warnings: global networks %d, core networks %d; want 2 each (no id, no ARN)", *gnWarns, *coreWarns)
	}
}

// TestScanNMAll_PhaseErrorRunsLaterPhases pins that a failed parent listing
// does not stop the phases after it: its error is returned and every other
// phase's rows are stored.
func TestScanNMAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	for _, c := range []struct {
		failOp   string
		stillHas []string
	}{
		{"DescribeGlobalNetworks", []string{TypeNetworkManagerCoreNetwork, TypeNetworkManagerVpcAttachment, TypeNetworkManagerCoreNetworkPrefixListAssociation}},
		{"ListCoreNetworks", []string{TypeNetworkManagerGlobalNetwork, TypeNetworkManagerSite, TypeNetworkManagerConnection, TypeNetworkManagerConnectPeer}},
		{"GetSites", []string{TypeNetworkManagerDevice, TypeNetworkManagerConnectPeer, TypeNetworkManagerCoreNetworkPrefixListAssociation}},
		{"ListAttachments", []string{TypeNetworkManagerTransitGatewayPeering, TypeNetworkManagerCoreNetworkPrefixListAssociation}},
	} {
		t.Run(c.failOp, func(t *testing.T) {
			h := nmAllHandlers(t)
			h[c.failOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			_, err := nmScanAll(t, st, h)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, c.failOp)
			}
			for _, typ := range c.stillHas {
				if len(rowsByNativeID(t, st, typ)) == 0 {
					t.Errorf("%s not stored after %s failed", typ, c.failOp)
				}
			}
		})
	}
}

// TestScanNMAll_StoreWriteStops pins that a failed write stops the scan,
// whichever phase it hits: no phase after it calls AWS.
func TestScanNMAll_StoreWriteStops(t *testing.T) {
	for _, c := range []struct {
		closeIn  string
		notAfter []string
	}{
		{"DescribeGlobalNetworks", []string{"ListCoreNetworks", "ListAttachments"}},
		{"ListCoreNetworks", []string{"GetSites", "ListAttachments"}},
		{"GetSites", []string{"GetDevices", "ListAttachments"}},
	} {
		t.Run(c.closeIn, func(t *testing.T) {
			calls := map[string]int{}
			h := nmAllHandlers(t)
			for op, f := range h {
				h[op] = func(in any) (any, error) {
					calls[op]++
					return f(in)
				}
			}
			st, closeStore := openClosingStore(t)
			serve := h[c.closeIn]
			h[c.closeIn] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			if _, err := nmScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			for _, op := range c.notAfter {
				if calls[op] != 0 {
					t.Errorf("%s called %d times after the failed write, want 0", op, calls[op])
				}
			}
		})
	}
}

// TestScanNMAll_NoChildIDWarnsOnlyAfterWrite pins that a parent stored without
// an id is not warned about when the write failed, since the warning would
// claim it was stored.
func TestScanNMAll_NoChildIDWarnsOnlyAfterWrite(t *testing.T) {
	for _, c := range []struct{ op, label string }{
		{"DescribeGlobalNetworks", nmDescribeGlobalNetworksOp},
		{"ListCoreNetworks", nmListCoreNetworksOp},
	} {
		t.Run(c.op, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			warns := countOpWarnings(st, c.label)
			h := nmAllHandlers(t)
			h["DescribeGlobalNetworks"] = func(any) (any, error) {
				return &networkmanager.DescribeGlobalNetworksOutput{GlobalNetworks: []nmtypes.GlobalNetwork{{GlobalNetworkArn: ptrStr(nmTestGN1)}}}, nil
			}
			h["ListCoreNetworks"] = func(any) (any, error) {
				return &networkmanager.ListCoreNetworksOutput{CoreNetworks: []nmtypes.CoreNetworkSummary{{CoreNetworkArn: ptrStr(nmTestCore1)}}}, nil
			}
			serve := h[c.op]
			h[c.op] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			if _, err := nmScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d, want 0", *warns)
			}
		})
	}
}

// TestScanNMAll_TransientBesideHardError pins that a throttled phase beside a
// hard-failing one is demoted to one warning and the scan classified as an
// error, not as transient.
func TestScanNMAll_TransientBesideHardError(t *testing.T) {
	h := nmAllHandlers(t)
	h["DescribeGlobalNetworks"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListAttachments"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }
	st := newTestStore(t)
	warns := countOpWarnings(st, nmDescribeGlobalNetworksOp)
	_, err := nmScanAll(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d, want 1", *warns)
	}
}
