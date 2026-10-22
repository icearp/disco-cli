package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	rsDenied   = &smithy.GenericAPIError{Code: "AccessDenied", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: redshift:Describe"}
	rsBadInput = &smithy.GenericAPIError{Code: "InvalidParameterValue", Message: "bad"}
	rsStart    = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
)

func rsClient(handlers map[string]func(in any) (any, error)) *redshift.Client {
	return redshift.NewFromConfig(opStubConfig(handlers, rsDenied))
}

func rsClusterARN(id string) string { return redshiftClusterARN(testRegion, testAccountID, id) }

func rsClusters(ids ...string) []childParent {
	var out []childParent
	for _, id := range ids {
		out = append(out, childParent{id: id, arn: rsClusterARN(id)})
	}
	return out
}

func assertAPICode(t *testing.T, err error, code string) {
	t.Helper()
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != code {
		t.Fatalf("err = %v, want API error %s", err, code)
	}
}

// rsListCase drives one unpaginated list phase: its one page holds two
// elements and one without an id.
type rsListCase struct {
	op, typ              string
	skipCodes, warnCodes []string
	handler              func(fail error) func(in any) (any, error)
	empty                any
	wantIDs              []string
	run                  func(context.Context, redshiftSatelliteAPI, *store.Store) (int, error)
}

func rsListCases() []rsListCase {
	acct := newTestAccount(testAccountID)
	runner := func(f func(context.Context, redshiftSatelliteAPI, *account, string, *store.Store, string) (int, int, error)) func(context.Context, redshiftSatelliteAPI, *store.Store) (int, error) {
		return func(ctx context.Context, c redshiftSatelliteAPI, st *store.Store) (int, error) {
			total, _, err := f(ctx, c, acct, testRegion, st, testScanID)
			return total, err
		}
	}
	return []rsListCase{
		{
			op: "DescribeAuthenticationProfiles", typ: TypeRedshiftAuthenticationProfile,
			skipCodes: []string{"AuthenticationProfileNotFoundFault"},
			handler: func(fail error) func(in any) (any, error) {
				return func(any) (any, error) {
					if fail != nil {
						return nil, fail
					}
					return &redshift.DescribeAuthenticationProfilesOutput{AuthenticationProfiles: []redshifttypes.AuthenticationProfile{
						{AuthenticationProfileName: ptrStr("p1"), AuthenticationProfileContent: ptrStr(`{"Password":"hunter2"}`)},
						{AuthenticationProfileName: ptrStr("p2")},
						{AuthenticationProfileContent: ptrStr("{}")},
					}}, nil
				}
			},
			empty: &redshift.DescribeAuthenticationProfilesOutput{},
			wantIDs: []string{
				redshiftARN(testRegion, testAccountID, "authenticationprofile", "p1"),
				redshiftARN(testRegion, testAccountID, "authenticationprofile", "p2"),
			},
			run: runner(scanRedshiftAuthenticationProfiles),
		},
	}
}

// rsReservedNodesHandler answers DescribeReservedNodes with one node on page
// one and, on page two (Marker p2), one more plus one without an id.
func rsReservedNodesHandler(in any) (any, error) {
	switch sv(in.(*redshift.DescribeReservedNodesInput).Marker) {
	case "":
		return &redshift.DescribeReservedNodesOutput{Marker: ptrStr("p2"), ReservedNodes: []redshifttypes.ReservedNode{
			{ReservedNodeId: ptrStr("rn-1"), State: ptrStr("active"), StartTime: &rsStart, NodeType: ptrStr("ra3.xlplus")},
		}}, nil
	case "p2":
		return &redshift.DescribeReservedNodesOutput{ReservedNodes: []redshifttypes.ReservedNode{
			{ReservedNodeId: ptrStr("rn-2")}, {State: ptrStr("active")},
		}}, nil
	default:
		return nil, rsBadInput
	}
}

// rsQev2Handler answers DescribeQev2IdcApplications with one application on
// page one and, on page two (Marker p2), one more plus one without an ARN.
func rsQev2Handler(in any) (any, error) {
	switch sv(in.(*redshift.DescribeQev2IdcApplicationsInput).Marker) {
	case "":
		return &redshift.DescribeQev2IdcApplicationsOutput{Marker: ptrStr("p2"), Qev2IdcApplications: []redshifttypes.Qev2IdcApplication{
			{Qev2IdcApplicationArn: ptrStr("arn:aws:redshift:us-east-1:123456789012:qev2idcapplication:q1"), Qev2IdcApplicationName: ptrStr("q1"), IdcOnboardStatus: ptrStr("Completed"), IdcInstanceArn: ptrStr("arn:aws:sso:::instance/ssoins-1")},
		}}, nil
	case "p2":
		return &redshift.DescribeQev2IdcApplicationsOutput{Qev2IdcApplications: []redshifttypes.Qev2IdcApplication{
			{Qev2IdcApplicationArn: ptrStr("arn:aws:redshift:us-east-1:123456789012:qev2idcapplication:q2")}, {Qev2IdcApplicationName: ptrStr("no-arn")},
		}}, nil
	default:
		return nil, rsBadInput
	}
}

func TestScanRedshiftListPhases_StoreEveryElementWarnIDless(t *testing.T) {
	for _, c := range rsListCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "redshift:"+c.op)
			total, err := c.run(context.Background(), rsClient(map[string]func(any) (any, error){c.op: c.handler(nil)}), st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, c.typ)
			var got []string
			for id, r := range rows {
				got = append(got, id)
				if sv(r.Region) != testRegion {
					t.Errorf("%s region = %q, want %s", id, sv(r.Region), testRegion)
				}
			}
			slices.Sort(got)
			if total != len(c.wantIDs) || !slices.Equal(got, c.wantIDs) {
				t.Fatalf("total = %d, rows = %v, want %v", total, got, c.wantIDs)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for the element without an id", *warns)
			}
		})
	}
}

func TestScanRedshiftListPhases_Empty(t *testing.T) {
	for _, c := range rsListCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			client := rsClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return c.empty, nil }})
			total, err := c.run(context.Background(), client, st)
			if err != nil || total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Fatalf("total = %d, err = %v; want 0 rows, nil", total, err)
			}
		})
	}
}

type rsFailCase struct {
	name     string
	err      error
	wantWarn int
	wantErr  bool
}

// rsFailCases is a denial (warns), each warn code (warns), each skip code
// (silent) and an unmodeled code (fails the phase).
func rsFailCases(skip, warn []string) []rsFailCase {
	cases := []rsFailCase{{name: "denied", err: rsDenied, wantWarn: 1}}
	for _, code := range warn {
		cases = append(cases, rsFailCase{name: "warn " + code, err: &smithy.GenericAPIError{Code: code}, wantWarn: 1})
	}
	for _, code := range skip {
		cases = append(cases, rsFailCase{name: "skip " + code, err: &smithy.GenericAPIError{Code: code}})
	}
	return append(cases, rsFailCase{name: "other", err: rsBadInput, wantErr: true})
}

// A denial or warn code warns once, a skip code is silent; neither stores a
// row. Any other error fails the phase.
func TestScanRedshiftListPhases_FailedPage(t *testing.T) {
	for _, c := range rsListCases() {
		for _, tc := range rsFailCases(c.skipCodes, c.warnCodes) {
			t.Run(c.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "redshift:"+c.op)
				total, err := c.run(context.Background(), rsClient(map[string]func(any) (any, error){c.op: c.handler(tc.err)}), st)
				if *warns != tc.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
				}
				if tc.wantErr {
					assertAPICode(t, err, "InvalidParameterValue")
					return
				}
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
					t.Errorf("total = %d, want no rows", total)
				}
			})
		}
	}
}

func TestScanRedshiftListPhases_RowFields(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	client := rsClient(map[string]func(any) (any, error){
		"DescribeAuthenticationProfiles": rsListCases()[0].handler(nil),
		"DescribeReservedNodes":          rsReservedNodesHandler,
		"DescribeQev2IdcApplications":    rsQev2Handler,
	})
	for _, f := range []func(context.Context, redshiftSatelliteAPI, *account, string, *store.Store, string) (int, int, error){
		scanRedshiftAuthenticationProfiles, scanRedshiftReservedNodes, scanRedshiftQev2IdcApplications,
	} {
		if _, _, err := f(context.Background(), client, acct, testRegion, st, testScanID); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}

	profile := rowsByNativeID(t, st, TypeRedshiftAuthenticationProfile)[redshiftARN(testRegion, testAccountID, "authenticationprofile", "p1")]
	var pAttrs map[string]string
	if err := json.Unmarshal([]byte(profile.AttributesJSON), &pAttrs); err != nil {
		t.Fatalf("profile attrs: %v", err)
	}
	if sv(profile.Name) != "p1" || pAttrs["AuthenticationProfileName"] != "p1" || pAttrs["AuthenticationProfileContent"] != redact.Placeholder {
		t.Errorf("profile name = %q, attrs = %v; want p1 with content redacted", sv(profile.Name), pAttrs)
	}

	node := rowsByNativeID(t, st, TypeRedshiftReservedNode)[redshiftARN(testRegion, testAccountID, "reservednode", "rn-1")]
	if sv(node.Name) != "rn-1" || sv(node.Status) != "active" || sv(node.CreatedAt) != sv(tp(&rsStart)) {
		t.Errorf("reserved node name/status/created = %q/%q/%q", sv(node.Name), sv(node.Status), sv(node.CreatedAt))
	}
	if !jsonHasString(t, node.AttributesJSON, "NodeType", "ra3.xlplus") {
		t.Errorf("reserved node attrs = %s, want NodeType ra3.xlplus", node.AttributesJSON)
	}
	if bare := rowsByNativeID(t, st, TypeRedshiftReservedNode)[redshiftARN(testRegion, testAccountID, "reservednode", "rn-2")]; bare.Status != nil {
		t.Errorf("reserved node without state has status %q, want unset", sv(bare.Status))
	}

	app := rowsByNativeID(t, st, TypeRedshiftQev2IdcApplication)["arn:aws:redshift:us-east-1:123456789012:qev2idcapplication:q1"]
	if sv(app.Name) != "q1" || sv(app.Status) != "Completed" || !jsonHasString(t, app.AttributesJSON, "IdcInstanceArn", "arn:aws:sso:::instance/ssoins-1") {
		t.Errorf("qev2 app name/status = %q/%q, attrs = %s", sv(app.Name), sv(app.Status), app.AttributesJSON)
	}
}

func jsonHasString(t *testing.T, attrs, key, want string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(attrs), &m); err != nil {
		t.Fatalf("attrs %s: %v", attrs, err)
	}
	return m[key] == want
}

func rsDomainPage(next *string, assocs ...redshifttypes.Association) *redshift.DescribeCustomDomainAssociationsOutput {
	return &redshift.DescribeCustomDomainAssociationsOutput{Marker: next, Associations: assocs}
}

func rsAssoc(cert string, pairs ...[2]string) redshifttypes.Association {
	a := redshifttypes.Association{CustomDomainCertificateArn: ptrStr(cert)}
	for _, p := range pairs {
		a.CertificateAssociations = append(a.CertificateAssociations, redshifttypes.CertificateAssociation{ClusterIdentifier: ptrStr(p[0]), CustomDomainName: ptrStr(p[1])})
	}
	return a
}

const rsCert = "arn:aws:acm:us-east-1:123456789012:certificate/c1"

func rsDomainHandler(fail error) func(in any) (any, error) {
	return func(in any) (any, error) {
		switch sv(in.(*redshift.DescribeCustomDomainAssociationsInput).Marker) {
		case "":
			return rsDomainPage(ptrStr("p2"), rsAssoc(rsCert, [2]string{"c1", "a.example.com"}, [2]string{"c2", "b.example.com"})), nil
		case "p2":
		default:
			return nil, rsBadInput
		}
		if fail != nil {
			return nil, fail
		}
		return rsDomainPage(nil, rsAssoc("arn:aws:acm:us-east-1:123456789012:certificate/c2",
			[2]string{"c1", "c.example.com"}, [2]string{"gone", "d.example.com"}, [2]string{"c2", ""})), nil
	}
}

func TestScanRedshiftCustomDomainAssociations_PairsUnderStoredClusters(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c1"), testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c2"), testRegion, "{}")
	client := rsClient(map[string]func(any) (any, error){"DescribeCustomDomainAssociations": rsDomainHandler(nil)})
	warns := countOpWarnings(st, "redshift:DescribeCustomDomainAssociations")

	total, _, err := scanRedshiftCustomDomainAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("c1", "c2"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeRedshiftCustomDomainAssociation)
	want := []string{
		rsClusterARN("c1") + "/custom-domain-association/a.example.com",
		rsClusterARN("c1") + "/custom-domain-association/c.example.com",
		rsClusterARN("c2") + "/custom-domain-association/b.example.com",
	}
	var got []string
	for id := range rows {
		got = append(got, id)
	}
	slices.Sort(got)
	if total != 3 || !slices.Equal(got, want) {
		t.Fatalf("total = %d, rows = %v, want %v", total, got, want)
	}
	assertContainsNative(t, st, rsClusterARN("c1"), want[0])
	assertContainsNative(t, st, rsClusterARN("c2"), want[2])
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the pair without a domain", *warns)
	}

	row := rows[want[0]]
	var attrs redshifttypes.Association
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if sv(row.Name) != "a.example.com" || sv(row.Region) != testRegion || sv(attrs.CustomDomainCertificateArn) != rsCert ||
		len(attrs.CertificateAssociations) != 1 || sv(attrs.CertificateAssociations[0].CustomDomainName) != "a.example.com" {
		t.Errorf("row name = %q, attrs = %s; want only its own pair under %s", sv(row.Name), row.AttributesJSON, rsCert)
	}
}

func TestScanRedshiftCustomDomainAssociations_NoClustersNoCall(t *testing.T) {
	st := newTestStore(t)
	client := rsClient(map[string]func(any) (any, error){"DescribeCustomDomainAssociations": func(any) (any, error) {
		t.Fatal("DescribeCustomDomainAssociations called with no clusters")
		return nil, nil
	}})
	total, _, err := scanRedshiftCustomDomainAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Fatalf("total = %d, err = %v; want 0, nil", total, err)
	}
}

// A failed first page stores nothing: a skip code is silent, a denial warns,
// any other error fails the phase.
func TestScanRedshiftCustomDomainAssociations_FailedFirstPage(t *testing.T) {
	for _, tc := range rsFailCases([]string{"CustomDomainAssociationNotFoundFault", "UnsupportedOperation"}, nil) {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "redshift:DescribeCustomDomainAssociations")
			client := rsClient(map[string]func(any) (any, error){"DescribeCustomDomainAssociations": func(any) (any, error) { return nil, tc.err }})
			total, _, err := scanRedshiftCustomDomainAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("c1"))
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if tc.wantErr {
				assertAPICode(t, err, "InvalidParameterValue")
			} else if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if total != 0 {
				t.Errorf("total = %d, want 0", total)
			}
		})
	}
}

// A page failing after one was read keeps page one's pairs. A denial warns;
// anything else, a first-page skip code included, fails the phase.
func TestScanRedshiftCustomDomainAssociations_FailedLaterPageKeepsPairs(t *testing.T) {
	for _, tc := range []rsFailCase{
		{name: "denied", err: rsDenied, wantWarn: 1},
		{name: "skip code", err: &smithy.GenericAPIError{Code: "CustomDomainAssociationNotFoundFault"}, wantErr: true},
		{name: "other", err: rsBadInput, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c1"), testRegion, "{}")
			upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c2"), testRegion, "{}")
			warns := countOpWarnings(st, "redshift:DescribeCustomDomainAssociations")
			client := rsClient(map[string]func(any) (any, error){"DescribeCustomDomainAssociations": rsDomainHandler(tc.err)})
			total, _, err := scanRedshiftCustomDomainAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("c1", "c2"))
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			var ae smithy.APIError
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.As(err, &ae)) {
				t.Errorf("err = %v, want error %v", err, tc.wantErr)
			}
			if total != 2 || len(rowsByNativeID(t, st, TypeRedshiftCustomDomainAssociation)) != 2 {
				t.Errorf("total = %d, want page one's 2 rows", total)
			}
		})
	}
}

// A Marker repeated by the service ends the listing as incomplete instead of
// looping, after storing what was read.
func TestScanRedshiftCustomDomainAssociations_RepeatedMarker(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c1"), testRegion, "{}")
	client := rsClient(map[string]func(any) (any, error){"DescribeCustomDomainAssociations": func(in any) (any, error) {
		if in.(*redshift.DescribeCustomDomainAssociationsInput).Marker == nil {
			return rsDomainPage(ptrStr("p2"), rsAssoc(rsCert, [2]string{"c1", "a.example.com"})), nil
		}
		return rsDomainPage(ptrStr("p2"), rsAssoc(rsCert, [2]string{"c1", "c.example.com"})), nil
	}})
	total, _, err := scanRedshiftCustomDomainAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("c1"))
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v, want errRepeatedNextToken", err)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeRedshiftCustomDomainAssociation)) != 2 {
		t.Errorf("total = %d, want the 2 pairs read up to the repeat", total)
	}
}

func rsPartnerHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*redshift.DescribePartnersInput)
		id := sv(p.ClusterIdentifier)
		calls.add(id)
		if sv(p.AccountId) != testAccountID {
			return nil, rsBadInput
		}
		if err := fail[id]; err != nil {
			return nil, err
		}
		return &redshift.DescribePartnersOutput{PartnerIntegrationInfoList: []redshifttypes.PartnerIntegrationInfo{
			{DatabaseName: ptrStr("dev"), PartnerName: ptrStr("Fivetran"), Status: redshifttypes.PartnerIntegrationStatusActive, CreatedAt: &rsStart},
			{DatabaseName: ptrStr("dev")},
		}}, nil
	}
}

func rsPartnerID(cluster string) string { return rsClusterARN(cluster) + "/partner/dev/Fivetran" }

func TestScanRedshiftPartners_EveryClusterQueried(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c1"), testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("c2"), testRegion, "{}")
	var calls parentCalls
	client := rsClient(map[string]func(any) (any, error){"DescribePartners": rsPartnerHandler(&calls, nil)})

	total, _, err := scanRedshiftPartners(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("c1", "c2"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Errorf("clusters queried = %v, want [c1 c2]", got)
	}
	rows := rowsByNativeID(t, st, TypeRedshiftPartner)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v, want one partner per cluster", total, rows)
	}
	row, ok := rows[rsPartnerID("c1")]
	if !ok {
		t.Fatalf("missing %s in %v", rsPartnerID("c1"), rows)
	}
	if sv(row.Name) != "Fivetran" || sv(row.Status) != "Active" || sv(row.Region) != testRegion ||
		sv(row.CreatedAt) != sv(tp(&rsStart)) || !jsonHasString(t, row.AttributesJSON, "DatabaseName", "dev") {
		t.Errorf("row name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(row.Name), sv(row.Status), sv(row.Region), sv(row.CreatedAt), row.AttributesJSON)
	}
	assertContainsNative(t, st, rsClusterARN("c1"), rsPartnerID("c1"))
	assertContainsNative(t, st, rsClusterARN("c2"), rsPartnerID("c2"))
}

// A failed cluster drops only itself: a cluster deleted since listing is
// skipped silently, and so is UnsupportedOperation; a denial or a warn code
// warns once however many clusters hit it, and any other error fails the phase.
func TestScanRedshiftPartners_FailedCluster(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantWarn int
	}{
		{"cluster gone", &smithy.GenericAPIError{Code: "ClusterNotFound"}, 0},
		{"unsupported", &smithy.GenericAPIError{Code: "UnsupportedOperation"}, 0},
		{"denied", rsDenied, 1},
		{"unauthorized partner", &smithy.GenericAPIError{Code: "UnauthorizedPartnerIntegration"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftCluster, rsClusterARN("ok"), testRegion, "{}")
			warns := countOpWarnings(st, "redshift:DescribePartners")
			var calls parentCalls
			client := rsClient(map[string]func(any) (any, error){"DescribePartners": rsPartnerHandler(&calls, map[string]error{"bad1": tc.err, "bad2": tc.err})})
			total, _, err := scanRedshiftPartners(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("bad1", "ok", "bad2"))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if _, ok := rowsByNativeID(t, st, TypeRedshiftPartner)[rsPartnerID("ok")]; total != 1 || !ok {
				t.Errorf("total = %d, want the healthy cluster's partner stored", total)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		client := rsClient(map[string]func(any) (any, error){"DescribePartners": rsPartnerHandler(&calls, map[string]error{"bad": rsBadInput})})
		_, _, err := scanRedshiftPartners(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rsClusters("bad"))
		assertAPICode(t, err, "InvalidParameterValue")
	})
}

// rsTopLevelHandlers answers the cluster list, the last pre-existing phase
// (data shares) and the satellite ops; every other op is denied and
// warn-skipped by its phase.
func rsTopLevelHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeClusters": func(any) (any, error) {
			return &redshift.DescribeClustersOutput{Clusters: []redshifttypes.Cluster{{ClusterIdentifier: ptrStr("c1")}}}, nil
		},
		"DescribeDataShares": func(any) (any, error) {
			return &redshift.DescribeDataSharesOutput{DataShares: []redshifttypes.DataShare{{DataShareArn: ptrStr("arn:aws:redshift:us-east-1:123456789012:datashare:ns/share1")}}}, nil
		},
		"DescribeAuthenticationProfiles": rsListCases()[0].handler(nil),
		"DescribeReservedNodes":          rsReservedNodesHandler,
		"DescribeQev2IdcApplications":    rsQev2Handler,
		"DescribeCustomDomainAssociations": func(any) (any, error) {
			return rsDomainPage(nil, rsAssoc(rsCert, [2]string{"c1", "a.example.com"})), nil
		},
		"DescribePartners": rsPartnerHandler(calls, nil),
	}
}

func TestScanRedshift_StoresSatellitesUnderListedClusters(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	acct.cfg = opStubConfig(rsTopLevelHandlers(&calls), rsDenied)

	if _, _, err := scanRedshift(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanRedshift: %v", err)
	}
	for typ, n := range map[string]int{
		TypeRedshiftAuthenticationProfile:   2,
		TypeRedshiftReservedNode:            2,
		TypeRedshiftQev2IdcApplication:      2,
		TypeRedshiftCustomDomainAssociation: 1,
		TypeRedshiftPartner:                 1,
	} {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d, want %d", typ, got, n)
		}
	}
	assertContainsNative(t, st, rsClusterARN("c1"), rsClusterARN("c1")+"/custom-domain-association/a.example.com")
	assertContainsNative(t, st, rsClusterARN("c1"), rsPartnerID("c1"))
}

// A region-wide satellite phase that hits a skip code ends quietly, and the
// phases after it still run: the cluster satellites, last in order, are stored.
func TestScanRedshift_SkipCodeLetsLaterPhasesRun(t *testing.T) {
	for _, tc := range []struct{ op, code string }{
		{"DescribeAuthenticationProfiles", "AuthenticationProfileNotFoundFault"},
		{"DescribeReservedNodes", "ReservedNodeNotFound"},
		{"DescribeQev2IdcApplications", "Qev2IdcApplicationNotExists"},
		{"DescribeQev2IdcApplications", "UnsupportedOperation"},
		{"DescribeCustomDomainAssociations", "CustomDomainAssociationNotFoundFault"},
		{"DescribeCustomDomainAssociations", "UnsupportedOperation"},
	} {
		t.Run(tc.op+"/"+tc.code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "redshift:"+tc.op)
			acct := newTestAccount(testAccountID)
			var calls parentCalls
			handlers := rsTopLevelHandlers(&calls)
			handlers[tc.op] = func(any) (any, error) { return nil, &smithy.GenericAPIError{Code: tc.code} }
			acct.cfg = opStubConfig(handlers, rsDenied)

			if _, _, err := scanRedshift(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanRedshift: %v", err)
			}
			if *warns != 0 {
				t.Errorf("warnings for %s = %d, want 0", tc.op, *warns)
			}
			if _, ok := rowsByNativeID(t, st, TypeRedshiftPartner)[rsPartnerID("c1")]; !ok {
				t.Errorf("partner row missing: the phases after %s did not run", tc.op)
			}
		})
	}
}

// Each satellite phase runs after every pre-existing phase, and its hard error
// reaches the caller.
func TestScanRedshift_SatelliteErrorAfterExistingPhases(t *testing.T) {
	for _, op := range []string{
		"DescribeAuthenticationProfiles", "DescribeReservedNodes", "DescribeQev2IdcApplications",
		"DescribeCustomDomainAssociations", "DescribePartners",
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var calls parentCalls
			handlers := rsTopLevelHandlers(&calls)
			var failed atomic.Bool
			handlers[op] = func(any) (any, error) {
				failed.Store(true)
				return nil, rsBadInput
			}
			acct.cfg = opStubConfig(handlers, rsDenied)

			_, _, err := scanRedshift(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", op)
			}
			assertAPICode(t, err, "InvalidParameterValue")
			if len(rowsByNativeID(t, st, TypeRedshiftDatashare)) != 1 {
				t.Errorf("data share row missing: %s ran before the last pre-existing phase", op)
			}
		})
	}
}
