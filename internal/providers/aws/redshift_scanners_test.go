package aws

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// rsElem is one listed element of a list phase: key is its identifier, ""
// for an element without one.
type rsElem struct {
	key, status string
	tags        []redshifttypes.Tag
}

// rsListedCase is one paginated region-wide list phase. page builds the op's
// output; nativeID is the row an element with key is stored under; skip and
// warn are the codes a failed first page tolerates. tagged and hasStatus say
// whether the listed element carries Tags and a status the row stores.
type rsListedCase struct {
	op, typ           string
	skip, warn        []string
	tagged, hasStatus bool
	page              func(next *string, elems ...rsElem) any
	nativeID          func(key string) string
	run               func(context.Context, *redshift.Client, *store.Store) (int, int, error)
}

var rsTags = []redshifttypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}}

func rsKey(key string) *string {
	if key == "" {
		return nil
	}
	return &key
}

// rsOptARN gives an element with key its ARN of kind; one without a key gets
// none.
func rsOptARN(kind string) func(key string) *string {
	return func(key string) *string {
		if key == "" {
			return nil
		}
		return ptrStr(redshiftARN("aws", testRegion, testAccountID, kind, key))
	}
}

func rsKindID(kind string) func(key string) string {
	return func(key string) string { return redshiftARN("aws", testRegion, testAccountID, kind, key) }
}

// rsRun adapts a list phase to the case's runner, whatever narrow client
// interface it takes.
func rsRun[C any](f func(context.Context, C, *account, string, *store.Store, string) (int, int, error)) func(context.Context, *redshift.Client, *store.Store) (int, int, error) {
	return func(ctx context.Context, c *redshift.Client, st *store.Store) (int, int, error) {
		return f(ctx, any(c).(C), newTestAccount(testAccountID), testRegion, st, testScanID)
	}
}

func rsListedCases() []rsListedCase {
	idcARN := rsOptARN("redshiftidcapplication")
	qevARN := rsOptARN("qev2idcapplication")
	intARN := rsOptARN("integration")
	snapARN := rsOptARN("snapshot")
	shareARN := rsOptARN("datashare")
	return []rsListedCase{
		{
			op: "DescribeClusters", hasStatus: true, typ: TypeRedshiftCluster, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeClustersOutput{Marker: next}
				for _, e := range es {
					o.Clusters = append(o.Clusters, redshifttypes.Cluster{ClusterIdentifier: rsKey(e.key), ClusterStatus: rsKey(e.status), Tags: e.tags})
				}
				return o
			},
			nativeID: rsClusterARN,
			run: func(ctx context.Context, c *redshift.Client, st *store.Store) (int, int, error) {
				t, i, _, err := scanRedshiftClusters(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID)
				return t, i, err
			},
		},
		{
			op: "DescribeClusterSubnetGroups", hasStatus: true, typ: TypeRedshiftSubnetGroup, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeClusterSubnetGroupsOutput{Marker: next}
				for _, e := range es {
					o.ClusterSubnetGroups = append(o.ClusterSubnetGroups, redshifttypes.ClusterSubnetGroup{ClusterSubnetGroupName: rsKey(e.key), SubnetGroupStatus: rsKey(e.status), Tags: e.tags})
				}
				return o
			},
			nativeID: func(key string) string { return redshiftSubnetGroupARN("aws", testRegion, testAccountID, key) },
			run:      rsRun(scanRedshiftClusterSubnetGroups),
		},
		{
			op: "DescribeClusterParameterGroups", typ: TypeRedshiftClusterParameterGroup, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeClusterParameterGroupsOutput{Marker: next}
				for _, e := range es {
					o.ParameterGroups = append(o.ParameterGroups, redshifttypes.ClusterParameterGroup{ParameterGroupName: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("parametergroup"),
			run:      rsRun(scanRedshiftClusterParameterGroups),
		},
		{
			op: "DescribeEventSubscriptions", typ: TypeRedshiftEventSubscription, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeEventSubscriptionsOutput{Marker: next}
				for _, e := range es {
					o.EventSubscriptionsList = append(o.EventSubscriptionsList, redshifttypes.EventSubscription{CustSubscriptionId: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("eventsubscription"),
			run:      rsRun(scanRedshiftEventSubscriptions),
		},
		{
			op: "DescribeIntegrations", typ: TypeRedshiftIntegration, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeIntegrationsOutput{Marker: next}
				for _, e := range es {
					o.Integrations = append(o.Integrations, redshifttypes.Integration{IntegrationArn: intARN(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("integration"),
			run:      rsRun(scanRedshiftIntegrations),
		},
		{
			op: "DescribeClusterSnapshots", hasStatus: true, typ: TypeRedshiftSnapshot, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeClusterSnapshotsOutput{Marker: next}
				for _, e := range es {
					o.Snapshots = append(o.Snapshots, redshifttypes.Snapshot{SnapshotArn: snapARN(e.key), SnapshotIdentifier: rsKey(e.key), Status: rsKey(e.status), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("snapshot"),
			run:      rsRun(scanRedshiftSnapshots),
		},
		{
			op: "DescribeSnapshotSchedules", typ: TypeRedshiftSnapshotSchedule, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeSnapshotSchedulesOutput{Marker: next}
				for _, e := range es {
					o.SnapshotSchedules = append(o.SnapshotSchedules, redshifttypes.SnapshotSchedule{ScheduleIdentifier: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("snapshotschedule"),
			run:      rsRun(scanRedshiftSnapshotSchedules),
		},
		{
			op: "DescribeSnapshotCopyGrants", typ: TypeRedshiftSnapshotCopyGrant, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeSnapshotCopyGrantsOutput{Marker: next}
				for _, e := range es {
					o.SnapshotCopyGrants = append(o.SnapshotCopyGrants, redshifttypes.SnapshotCopyGrant{SnapshotCopyGrantName: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("snapshotcopygrant"),
			run:      rsRun(scanRedshiftSnapshotCopyGrants),
		},
		{
			op: "DescribeUsageLimits", typ: TypeRedshiftUsageLimit, tagged: true,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeUsageLimitsOutput{Marker: next}
				for _, e := range es {
					o.UsageLimits = append(o.UsageLimits, redshifttypes.UsageLimit{UsageLimitId: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("usagelimit"),
			run:      rsRun(scanRedshiftUsageLimits),
		},
		{
			op: "DescribeHsmClientCertificates", typ: TypeRedshiftHsmClientCertificate, tagged: true,
			skip: []string{"UnsupportedOperation"},
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeHsmClientCertificatesOutput{Marker: next}
				for _, e := range es {
					o.HsmClientCertificates = append(o.HsmClientCertificates, redshifttypes.HsmClientCertificate{HsmClientCertificateIdentifier: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("hsmclientcertificate"),
			run:      rsRun(scanRedshiftHsmClientCertificates),
		},
		{
			op: "DescribeHsmConfigurations", typ: TypeRedshiftHsmConfiguration, tagged: true,
			skip: []string{"UnsupportedOperation"},
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeHsmConfigurationsOutput{Marker: next}
				for _, e := range es {
					o.HsmConfigurations = append(o.HsmConfigurations, redshifttypes.HsmConfiguration{HsmConfigurationIdentifier: rsKey(e.key), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("hsmconfiguration"),
			run:      rsRun(scanRedshiftHsmConfigurations),
		},
		{
			op: "DescribeRedshiftIdcApplications", hasStatus: true, typ: TypeRedshiftIdcApplication, tagged: true,
			skip: []string{"RedshiftIdcApplicationNotExists", "UnsupportedOperation"},
			warn: []string{"DependentServiceAccessDenied", "DependentServiceUnavailableFault"},
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeRedshiftIdcApplicationsOutput{Marker: next}
				for _, e := range es {
					o.RedshiftIdcApplications = append(o.RedshiftIdcApplications, redshifttypes.RedshiftIdcApplication{RedshiftIdcApplicationArn: idcARN(e.key), IdcOnboardStatus: rsKey(e.status), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("redshiftidcapplication"),
			run:      rsRun(scanRedshiftIdcApplications),
		},
		{
			op: "DescribeQev2IdcApplications", hasStatus: true, typ: TypeRedshiftQev2IdcApplication, tagged: true,
			skip: []string{"Qev2IdcApplicationNotExists", "UnsupportedOperation"},
			warn: []string{"DependentServiceAccessDenied", "DependentServiceUnavailableFault"},
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeQev2IdcApplicationsOutput{Marker: next}
				for _, e := range es {
					o.Qev2IdcApplications = append(o.Qev2IdcApplications, redshifttypes.Qev2IdcApplication{Qev2IdcApplicationArn: qevARN(e.key), IdcOnboardStatus: rsKey(e.status), Tags: e.tags})
				}
				return o
			},
			nativeID: rsKindID("qev2idcapplication"),
			run:      rsRun(scanRedshiftQev2IdcApplications),
		},
		{
			op: "DescribeEndpointAccess", typ: TypeRedshiftEndpointAccess,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeEndpointAccessOutput{Marker: next}
				for _, e := range es {
					o.EndpointAccessList = append(o.EndpointAccessList, redshifttypes.EndpointAccess{EndpointName: rsKey(e.key)})
				}
				return o
			},
			nativeID: rsKindID("endpointaccess"),
			run:      rsRun(scanRedshiftEndpointAccess),
		},
		{
			// The element without an id has a cluster and grantee but no
			// grantor: any missing part of the key leaves it unstored.
			op: "DescribeEndpointAuthorization", typ: TypeRedshiftEndpointAuthorization,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeEndpointAuthorizationOutput{Marker: next}
				for _, e := range es {
					a := redshifttypes.EndpointAuthorization{ClusterIdentifier: ptrStr("orphan"), Grantee: ptrStr("222222222222")}
					if e.key != "" {
						a.ClusterIdentifier, a.Grantor = ptrStr(e.key), ptrStr("111111111111")
					}
					o.EndpointAuthorizationList = append(o.EndpointAuthorizationList, a)
				}
				return o
			},
			nativeID: func(key string) string {
				return redshiftARN("aws", testRegion, testAccountID, "endpointauthorization", key+"/111111111111/222222222222")
			},
			run: rsRun(scanRedshiftEndpointAuthorization),
		},
		{
			op: "DescribeScheduledActions", typ: TypeRedshiftScheduledAction,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeScheduledActionsOutput{Marker: next}
				for _, e := range es {
					o.ScheduledActions = append(o.ScheduledActions, redshifttypes.ScheduledAction{ScheduledActionName: rsKey(e.key)})
				}
				return o
			},
			nativeID: rsKindID("scheduledaction"),
			run:      rsRun(scanRedshiftScheduledActions),
		},
		{
			op: "DescribeDataShares", typ: TypeRedshiftDatashare,
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeDataSharesOutput{Marker: next}
				for _, e := range es {
					o.DataShares = append(o.DataShares, redshifttypes.DataShare{DataShareArn: shareARN(e.key)})
				}
				return o
			},
			nativeID: rsKindID("datashare"),
			run:      rsRun(scanRedshiftDataShares),
		},
		{
			op: "DescribeReservedNodes", hasStatus: true, typ: TypeRedshiftReservedNode,
			skip: []string{"ReservedNodeNotFound"}, warn: []string{"DependentServiceUnavailableFault"},
			page: func(next *string, es ...rsElem) any {
				o := &redshift.DescribeReservedNodesOutput{Marker: next}
				for _, e := range es {
					o.ReservedNodes = append(o.ReservedNodes, redshifttypes.ReservedNode{ReservedNodeId: rsKey(e.key), State: rsKey(e.status)})
				}
				return o
			},
			nativeID: rsKindID("reservednode"),
			run:      rsRun(scanRedshiftReservedNodes),
		},
	}
}

// rsMarker reads the Marker every paginated Redshift Describe* input carries,
// and rejects an input that sets anything else: no filter narrows the listing.
func rsMarker(in any) (*string, error) {
	v := reflect.ValueOf(in).Elem()
	rest := reflect.New(v.Type()).Elem()
	rest.Set(v)
	rest.FieldByName("Marker").SetZero()
	if !rest.IsZero() {
		return nil, fmt.Errorf("unexpected input fields in %+v", in)
	}
	return v.FieldByName("Marker").Interface().(*string), nil
}

// rsListedHandler answers page one (no Marker) with a tagged element "a" that
// has a status, and Marker m2, and page two (Marker m2) with an untagged element "b" and one
// without an id. fail1 and fail2 fail the first and second page instead.
func rsListedHandler(c rsListedCase, fail1, fail2 error) func(in any) (any, error) {
	return func(in any) (any, error) {
		marker, err := rsMarker(in)
		if err != nil {
			return nil, err
		}
		switch m := sv(marker); m {
		case "":
			if fail1 != nil {
				return nil, fail1
			}
			return c.page(ptrStr("m2"), rsElem{key: "a", status: "available", tags: rsTags}), nil
		case "m2":
			if fail2 != nil {
				return nil, fail2
			}
			return c.page(nil, rsElem{key: "b"}, rsElem{}), nil
		default:
			return nil, fmt.Errorf("unexpected Marker %q", m)
		}
	}
}

func rsListedClient(c rsListedCase, fail1, fail2 error) *redshift.Client {
	return rsClient(map[string]func(any) (any, error){c.op: rsListedHandler(c, fail1, fail2)})
}

// Every page is read by forwarding the Marker; the listed element's Tags land
// in TagsJSON, an untagged element stores none, a status is stored only when
// present, and an element without an id is not stored but warned about, named
// by its position.
func TestScanRedshiftLists_TagsEveryPageWarnIDless(t *testing.T) {
	for _, c := range rsListedCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var warnings []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == "redshift:"+c.op {
					warnings = append(warnings, w.Message)
				}
			}
			total, inserted, err := c.run(context.Background(), rsListedClient(c, nil, nil), st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, c.typ)
			var got []string
			for id := range rows {
				got = append(got, id)
			}
			slices.Sort(got)
			want := []string{c.nativeID("a"), c.nativeID("b")}
			if total != 2 || inserted != 2 || !slices.Equal(got, want) {
				t.Fatalf("total/inserted = %d/%d, rows = %v; want 2/2, %v", total, inserted, got, want)
			}
			if c.tagged {
				assertTagsMap(t, rows[want[0]].TagsJSON, map[string]string{"env": "prod"})
			}
			if tags := rows[want[1]].TagsJSON; tags != nil {
				t.Errorf("untagged element TagsJSON = %q, want nil", *tags)
			}
			if c.hasStatus {
				if got := sv(rows[want[0]].Status); got != "available" {
					t.Errorf("Status = %q, want available", got)
				}
				if status := rows[want[1]].Status; status != nil {
					t.Errorf("element without a status has Status %q, want unset", *status)
				}
			}
			if sv(rows[want[0]].Region) != testRegion {
				t.Errorf("region = %q, want %s", sv(rows[want[0]].Region), testRegion)
			}
			// The position is the only name an element without an id has.
			if len(warnings) != 1 || !strings.Contains(warnings[0], "page 2 element 2") {
				t.Errorf("warnings = %q, want one naming page 2 element 2", warnings)
			}
		})
	}
}

// A snapshot without an ARN is stored under one built from its cluster and
// snapshot identifiers.
func TestScanRedshiftSnapshots_NoARNFallsBackToIdentifiers(t *testing.T) {
	st := newTestStore(t)
	client := rsClient(map[string]func(any) (any, error){"DescribeClusterSnapshots": func(any) (any, error) {
		return &redshift.DescribeClusterSnapshotsOutput{Snapshots: []redshifttypes.Snapshot{
			{ClusterIdentifier: ptrStr("c1"), SnapshotIdentifier: ptrStr("s1")},
		}}, nil
	}})
	if _, _, err := scanRedshiftSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := "arn:aws:redshift:us-east-1:123456789012:snapshot:c1/s1"
	if row, ok := rowsByNativeID(t, st, TypeRedshiftSnapshot)[want]; !ok || sv(row.Name) != "s1" {
		t.Errorf("snapshot rows = %v, want %s named s1", rowsByNativeID(t, st, TypeRedshiftSnapshot), want)
	}
}

// A failed first page stores nothing: a denial or a warn code warns, a skip
// code is silent, and any other error fails the phase.
func TestScanRedshiftLists_FailedFirstPage(t *testing.T) {
	for _, c := range rsListedCases() {
		for _, tc := range rsFailCases(c.skip, c.warn) {
			t.Run(c.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "redshift:"+c.op)
				total, _, err := c.run(context.Background(), rsListedClient(c, tc.err, nil), st)
				if *warns != tc.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
				}
				if tc.wantErr {
					assertAPICode(t, err, "InvalidParameterValue")
				} else if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				if total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
					t.Errorf("total = %d, want no rows", total)
				}
			})
		}
	}
}

// A page failing after one was read keeps page one's row. A denial warns;
// anything else, a first-page skip code included, leaves the listing
// incomplete and fails the phase.
func TestScanRedshiftLists_FailedLaterPageKeepsRows(t *testing.T) {
	for _, c := range rsListedCases() {
		cases := []rsFailCase{{name: "denied", err: rsDenied, wantWarn: 1}, {name: "other", err: rsBadInput, wantErr: true}}
		for _, code := range append(slices.Clone(c.skip), c.warn...) {
			cases = append(cases, rsFailCase{name: "code " + code, err: &smithy.GenericAPIError{Code: code}, wantErr: true})
		}
		for _, tc := range cases {
			t.Run(c.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "redshift:"+c.op)
				total, _, err := c.run(context.Background(), rsListedClient(c, nil, tc.err), st)
				if *warns != tc.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
				}
				var ae smithy.APIError
				if tc.wantErr != (err != nil) || (tc.wantErr && !errors.As(err, &ae)) {
					t.Errorf("err = %v, want error %v", err, tc.wantErr)
				}
				rows := rowsByNativeID(t, st, c.typ)
				if _, ok := rows[c.nativeID("a")]; total != 1 || len(rows) != 1 || !ok {
					t.Errorf("total = %d, rows = %d; want page one's row %s", total, len(rows), c.nativeID("a"))
				}
			})
		}
	}
}

// A Marker repeated by the service ends the listing as incomplete instead of
// looping, after storing what was read.
func TestScanRedshiftLists_RepeatedMarker(t *testing.T) {
	c := rsListedCases()[0]
	client := rsClient(map[string]func(any) (any, error){c.op: func(in any) (any, error) {
		if in.(*redshift.DescribeClustersInput).Marker == nil {
			return c.page(ptrStr("m2"), rsElem{key: "a"}), nil
		}
		return c.page(ptrStr("m2"), rsElem{key: "b"}), nil
	}})
	st := newTestStore(t)
	total, _, err := c.run(context.Background(), client, st)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v, want errRepeatedNextToken", err)
	}
	if total != 2 || len(rowsByNativeID(t, st, c.typ)) != 2 {
		t.Errorf("total = %d, want the 2 rows read up to the repeat", total)
	}
}

// rsFailOp makes op fail with err in the top-level handlers and records
// whether it was called.
func rsFailOp(handlers map[string]func(any) (any, error), op string, err error) *bool {
	called := new(bool)
	handlers[op] = func(any) (any, error) {
		*called = true
		return nil, err
	}
	return called
}

// A hard error in any phase, the cluster phase included, does not stop the
// phases after it, and still reaches the caller.
func TestScanRedshift_PhaseErrorRunsLaterPhases(t *testing.T) {
	for _, op := range []string{"DescribeClusters", "DescribeClusterSubnetGroups", "DescribeClusterSnapshots"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var calls parentCalls
			handlers := rsTopLevelHandlers(&calls)
			called := rsFailOp(handlers, op, rsBadInput)
			acct.cfg = opStubConfig(handlers, rsDenied)

			_, _, err := scanRedshift(context.Background(), acct, testRegion, st, testScanID)
			if !*called {
				t.Fatalf("%s never called", op)
			}
			assertAPICode(t, err, "InvalidParameterValue")
			if got := len(rowsByNativeID(t, st, TypeRedshiftReservedNode)); got != 2 {
				t.Errorf("reserved node rows = %d, want 2: the phases after %s did not run", got, op)
			}
		})
	}
}

// A throttled phase beside a hard-failed one is demoted to one warning, so
// the scan is reported as failed rather than as a transient glitch.
func TestScanRedshift_TransientBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "redshift:DescribeClusters")
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	handlers := rsTopLevelHandlers(&calls)
	rsFailOp(handlers, "DescribeClusters", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"})
	rsFailOp(handlers, "DescribeSnapshotSchedules", rsBadInput)
	acct.cfg = opStubConfig(handlers, rsDenied)

	_, _, err := scanRedshift(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("DescribeClusters warnings = %d, want 1", *warns)
	}
}

// A failed store write stops the later phases: every write would fail too.
func TestScanRedshift_StoreWriteStopsLaterPhases(t *testing.T) {
	st, closeStore := openClosingStore(t)
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	handlers := rsTopLevelHandlers(&calls)
	handlers["DescribeClusters"] = func(any) (any, error) {
		closeStore()
		return &redshift.DescribeClustersOutput{Clusters: []redshifttypes.Cluster{{ClusterIdentifier: ptrStr("c1")}}}, nil
	}
	later := rsFailOp(handlers, "DescribeClusterSubnetGroups", rsBadInput)
	acct.cfg = opStubConfig(handlers, rsDenied)

	_, _, err := scanRedshift(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want ErrStoreWrite", err)
	}
	if *later {
		t.Error("DescribeClusterSubnetGroups ran after the cluster write failed")
	}
}
