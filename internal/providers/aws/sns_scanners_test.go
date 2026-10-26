package aws

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var snsDenied = apiErr("AuthorizationError", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: SNS:List")

const (
	snsAppARN1  = "arn:aws:sns:us-east-1:123456789012:app/GCM/app-one"
	snsAppARN2  = "arn:aws:sns:us-east-1:123456789012:app/APNS/app-two"
	snsTopicARN = "arn:aws:sns:us-east-1:123456789012:push-events"
	snsRoleARN  = "arn:aws:iam::123456789012:role/sns-feedback"
	snsPhone1   = "+12065550100"
	snsPhone2   = "+12065550101"
)

func snsApp(arn string) snstypes.PlatformApplication {
	return snstypes.PlatformApplication{
		PlatformApplicationArn: sdkaws.String(arn),
		Attributes:             map[string]string{"Enabled": "true", "EventEndpointCreated": snsTopicARN},
	}
}

func snsPhone(phone string) snstypes.PhoneNumberInformation {
	return snstypes.PhoneNumberInformation{
		PhoneNumber:     sdkaws.String(phone),
		Status:          sdkaws.String("ACTIVE"),
		Iso2CountryCode: sdkaws.String("US"),
		RouteType:       snstypes.RouteTypeTransactional,
		CreatedAt:       sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
}

func snsPhoneID(phone string) string {
	return snsOriginationNumberNativeID("aws", testRegion, testAccountID, phone)
}

// snsTwoPages maps a call's NextToken to the page it asks for: 0 without
// "p2", 1 with it. The page-1 call fails with secondErr when that is set.
func snsTwoPages(token *string, secondErr error) (page int, err error) {
	if sdkaws.ToString(token) != "p2" {
		return 0, nil
	}
	return 1, secondErr
}

func snsAppHandler(pages [][]snstypes.PlatformApplication, secondErr error) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){"ListPlatformApplications": func(in any) (any, error) {
		page, err := snsTwoPages(in.(*sns.ListPlatformApplicationsInput).NextToken, secondErr)
		if err != nil {
			return nil, err
		}
		out := &sns.ListPlatformApplicationsOutput{PlatformApplications: pages[page]}
		if page+1 < len(pages) {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}}
}

func snsPhoneHandler(pages [][]snstypes.PhoneNumberInformation, secondErr error, limits *[]int32) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){"ListOriginationNumbers": func(in any) (any, error) {
		input := in.(*sns.ListOriginationNumbersInput)
		if limits != nil {
			*limits = append(*limits, sdkaws.ToInt32(input.MaxResults))
		}
		page, err := snsTwoPages(input.NextToken, secondErr)
		if err != nil {
			return nil, err
		}
		out := &sns.ListOriginationNumbersOutput{PhoneNumbers: pages[page]}
		if page+1 < len(pages) {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}}
}

func snsClient(h map[string]func(any) (any, error)) *sns.Client {
	return sns.NewFromConfig(opStubConfig(h, snsDenied))
}

func TestSNSOriginationNumberNativeID(t *testing.T) {
	got := snsOriginationNumberNativeID("aws", "eu-west-1", "111122223333", snsPhone1)
	if want := "arn:aws:sns:eu-west-1:111122223333:origination-number/+12065550100"; got != want {
		t.Errorf("NativeID = %q; want %q", got, want)
	}
}

func TestScanSNSPlatformApplications_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	h := snsAppHandler([][]snstypes.PlatformApplication{
		{snsApp(snsAppARN1), {Attributes: map[string]string{"Enabled": "true"}}},
		{snsApp(snsAppARN2)},
	}, nil)

	total, _, err := scanSNSPlatformApplications(context.Background(), snsClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	rows := rowsByNativeID(t, st, TypeSNSPlatformApplication)
	if len(rows) != 2 {
		t.Fatalf("stored %d rows; want 2 (the ARN-less element skipped)", len(rows))
	}
	for arn, name := range map[string]string{snsAppARN1: "app-one", snsAppARN2: "app-two"} {
		r, ok := rows[arn]
		if !ok {
			t.Fatalf("row %s missing", arn)
		}
		if r.Type != TypeSNSPlatformApplication || sv(r.Name) != name || sv(r.Region) != testRegion {
			t.Errorf("row %s = type %q name %q region %q; want %q, %q, %q", arn, r.Type, sv(r.Name), sv(r.Region), TypeSNSPlatformApplication, name, testRegion)
		}
		var attrs snstypes.PlatformApplication
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Attributes["EventEndpointCreated"] != snsTopicARN {
			t.Errorf("attrs = %s (%v); want EventEndpointCreated kept", r.AttributesJSON, err)
		}
	}
}

func TestScanSNSOriginationNumbers_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var limits []int32
	h := snsPhoneHandler([][]snstypes.PhoneNumberInformation{
		{snsPhone(snsPhone1), {Status: sdkaws.String("ACTIVE")}},
		{snsPhone(snsPhone2)},
	}, nil, &limits)

	total, _, err := scanSNSOriginationNumbers(context.Background(), snsClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if len(limits) != 2 || limits[0] != 30 || limits[1] != 30 {
		t.Errorf("MaxResults per call = %v; want [30 30]", limits)
	}
	rows := rowsByNativeID(t, st, TypeSNSOriginationNumber)
	if len(rows) != 2 {
		t.Fatalf("stored %d rows; want 2 (the number-less element skipped)", len(rows))
	}
	for _, phone := range []string{snsPhone1, snsPhone2} {
		r, ok := rows[snsPhoneID(phone)]
		if !ok {
			t.Fatalf("row %s missing", snsPhoneID(phone))
		}
		if r.Type != TypeSNSOriginationNumber || sv(r.Name) != phone || sv(r.Region) != testRegion ||
			sv(r.Status) != "ACTIVE" || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
			t.Errorf("row %s = type %q name %q region %q status %q created %q", phone, r.Type, sv(r.Name), sv(r.Region), sv(r.Status), sv(r.CreatedAt))
		}
		var attrs struct{ Iso2CountryCode, RouteType string }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Iso2CountryCode != "US" || attrs.RouteType != "Transactional" {
			t.Errorf("attrs = %s (%v); want the SDK element", r.AttributesJSON, err)
		}
	}
}

// snsPhaseCase runs one of the two new phases against handlers.
type snsPhaseCase struct {
	name string
	op   string
	typ  string
	run  func(*sns.Client, *store.Store) (int, int, error)
	// handlers answers two pages of one row each, the second failing with secondErr.
	handlers func(secondErr error) map[string]func(any) (any, error)
	// repeated answers every call with one row and NextToken "same".
	repeated map[string]func(any) (any, error)
}

func snsPhaseCases(calls *atomic.Int32) []snsPhaseCase {
	acct := newTestAccount(testAccountID)
	return []snsPhaseCase{
		{
			name: "platform applications", op: snsListPlatformApplicationsOp, typ: TypeSNSPlatformApplication,
			run: func(c *sns.Client, st *store.Store) (int, int, error) {
				return scanSNSPlatformApplications(context.Background(), c, acct, testRegion, st, testScanID)
			},
			handlers: func(secondErr error) map[string]func(any) (any, error) {
				return snsAppHandler([][]snstypes.PlatformApplication{{snsApp(snsAppARN1)}, {snsApp(snsAppARN2)}}, secondErr)
			},
			repeated: map[string]func(any) (any, error){"ListPlatformApplications": func(any) (any, error) {
				if calls.Add(1) > 2 {
					return nil, apiErr("InvalidParameter", "paginator did not stop on the repeated token")
				}
				return &sns.ListPlatformApplicationsOutput{PlatformApplications: []snstypes.PlatformApplication{snsApp(snsAppARN1)}, NextToken: sdkaws.String("same")}, nil
			}},
		},
		{
			name: "origination numbers", op: snsListOriginationNumbersOp, typ: TypeSNSOriginationNumber,
			run: func(c *sns.Client, st *store.Store) (int, int, error) {
				return scanSNSOriginationNumbers(context.Background(), c, acct, testRegion, st, testScanID)
			},
			handlers: func(secondErr error) map[string]func(any) (any, error) {
				return snsPhoneHandler([][]snstypes.PhoneNumberInformation{{snsPhone(snsPhone1)}, {snsPhone(snsPhone2)}}, secondErr, nil)
			},
			repeated: map[string]func(any) (any, error){"ListOriginationNumbers": func(any) (any, error) {
				if calls.Add(1) > 2 {
					return nil, apiErr("InvalidParameter", "paginator did not stop on the repeated token")
				}
				return &sns.ListOriginationNumbersOutput{PhoneNumbers: []snstypes.PhoneNumberInformation{snsPhone(snsPhone1)}, NextToken: sdkaws.String("same")}, nil
			}},
		},
	}
}

func TestScanSNSNewPhases_Empty(t *testing.T) {
	for _, c := range snsPhaseCases(new(atomic.Int32)) {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := map[string]func(any) (any, error){
				"ListPlatformApplications": func(any) (any, error) { return &sns.ListPlatformApplicationsOutput{}, nil },
				"ListOriginationNumbers":   func(any) (any, error) { return &sns.ListOriginationNumbersOutput{}, nil },
			}
			total, _, err := c.run(snsClient(h), st)
			if err != nil || total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Errorf("total, err = %d, %v; want 0 rows, nil", total, err)
			}
		})
	}
}

func TestScanSNSNewPhases_Errors(t *testing.T) {
	notDeployed := apiErr("UnknownOperationException", "")
	hard := apiErr("InvalidParameter", "bad")
	for _, c := range snsPhaseCases(new(atomic.Int32)) {
		for _, tc := range []struct {
			name      string
			first     error // fails the first page
			second    error // fails the second page
			wantRows  int
			wantWarns int
			wantCode  string
		}{
			{name: "SNS denial first page", first: snsDenied, wantWarns: 1},
			{name: "generic denial first page", first: apiErr("AccessDeniedException", "User: x is not authorized to perform: y"), wantWarns: 1},
			{name: "denial later page keeps first", second: snsDenied, wantRows: 1, wantWarns: 1},
			{name: "not deployed first page", first: notDeployed},
			{name: "not deployed later page fails and keeps first", second: notDeployed, wantRows: 1, wantCode: "UnknownOperationException"},
			{name: "other error first page", first: hard, wantCode: "InvalidParameter"},
			{name: "other error later page keeps first", second: hard, wantRows: 1, wantCode: "InvalidParameter"},
		} {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.op)
				var client *sns.Client
				if tc.first != nil {
					client = sns.NewFromConfig(opStubConfig(nil, tc.first))
				} else {
					client = snsClient(c.handlers(tc.second))
				}
				total, _, err := c.run(client, st)
				if tc.wantCode == "" && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
					t.Fatalf("err = %v; want %s", err, tc.wantCode)
				}
				if total != tc.wantRows || len(rowsByNativeID(t, st, c.typ)) != tc.wantRows || *warns != tc.wantWarns {
					t.Errorf("total, stored, warnings = %d, %d, %d; want %d, %d, %d",
						total, len(rowsByNativeID(t, st, c.typ)), *warns, tc.wantRows, tc.wantRows, tc.wantWarns)
				}
			})
		}
	}
}

func TestScanSNSNewPhases_RepeatedTokenStops(t *testing.T) {
	var calls atomic.Int32
	for _, c := range snsPhaseCases(&calls) {
		t.Run(c.name, func(t *testing.T) {
			calls.Store(0)
			st := newTestStore(t)
			total, _, err := c.run(snsClient(c.repeated), st)
			if err != nil || calls.Load() != 2 || total != 2 {
				t.Errorf("calls, total, err = %d, %d, %v; want 2, 2, nil", calls.Load(), total, err)
			}
		})
	}
}

// Through the real awsquery deserializer: a listing that carries push
// credentials stores none of them, and keeps the non-secret attributes.
func TestScanSNSPlatformApplications_CredentialsNeverStored(t *testing.T) {
	const body = `<ListPlatformApplicationsResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/">
  <ListPlatformApplicationsResult>
    <PlatformApplications>
      <member>
        <PlatformApplicationArn>` + snsAppARN1 + `</PlatformApplicationArn>
        <Attributes>
          <entry><key>PlatformCredential</key><value>-----BEGIN PRIVATE KEY-----SECRETKEY123</value></entry>
          <entry><key>PlatformPrincipal</key><value>SECRETPRINCIPAL456</value></entry>
          <entry><key>SuccessFeedbackRoleArn</key><value>` + snsRoleARN + `</value></entry>
          <entry><key>AppleCertificateExpiryDate</key><value>2027-01-01T00:00:00Z</value></entry>
        </Attributes>
      </member>
    </PlatformApplications>
  </ListPlatformApplicationsResult>
  <ResponseMetadata><RequestId>req-1</RequestId></ResponseMetadata>
</ListPlatformApplicationsResponse>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	client := sns.NewFromConfig(sdkaws.Config{
		Region:           testRegion,
		Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		RetryMaxAttempts: 1,
	}, func(o *sns.Options) { o.BaseEndpoint = sdkaws.String(srv.URL) })

	st := newTestStore(t)
	total, _, err := scanSNSPlatformApplications(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("scan = %d rows, err %v; want 1 row", total, err)
	}
	r, ok := rowsByNativeID(t, st, TypeSNSPlatformApplication)[snsAppARN1]
	if !ok {
		t.Fatalf("row %s missing", snsAppARN1)
	}
	for col, v := range map[string]string{"attributes": r.AttributesJSON, "name": sv(r.Name), "tags": sv(r.TagsJSON), "status": sv(r.Status)} {
		if strings.Contains(v, "SECRETKEY123") || strings.Contains(v, "SECRETPRINCIPAL456") {
			t.Errorf("%s column holds a push credential: %s", col, v)
		}
	}
	var app snstypes.PlatformApplication
	if err := json.Unmarshal([]byte(r.AttributesJSON), &app); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PlatformCredential":         redact.Placeholder,
		"PlatformPrincipal":          redact.Placeholder,
		"SuccessFeedbackRoleArn":     snsRoleARN,
		"AppleCertificateExpiryDate": "2027-01-01T00:00:00Z",
	}
	for k, v := range want {
		if app.Attributes[k] != v {
			t.Errorf("Attributes[%s] = %q; want %q", k, app.Attributes[k], v)
		}
	}
}

func snsTopicHandlers(listTopicsErr error) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListTopics": func(any) (any, error) {
			if listTopicsErr != nil {
				return nil, listTopicsErr
			}
			return &sns.ListTopicsOutput{Topics: []snstypes.Topic{{TopicArn: sdkaws.String(snsTopicARN)}}}, nil
		},
		"GetTopicAttributes": func(any) (any, error) {
			return &sns.GetTopicAttributesOutput{Attributes: map[string]string{"TopicArn": snsTopicARN}}, nil
		},
		"ListSubscriptions": func(any) (any, error) { return &sns.ListSubscriptionsOutput{}, nil },
	}
}

func snsMerge(hs ...map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	out := map[string]func(any) (any, error){}
	for _, h := range hs {
		for k, v := range h {
			out[k] = v
		}
	}
	return out
}

func TestScanSNS_StoresEveryPhase(t *testing.T) {
	st := newTestStore(t)
	h := snsMerge(snsTopicHandlers(nil),
		snsAppHandler([][]snstypes.PlatformApplication{{snsApp(snsAppARN1)}}, nil),
		snsPhoneHandler([][]snstypes.PhoneNumberInformation{{snsPhone(snsPhone1)}}, nil, nil))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, snsDenied)}

	total, _, err := scanSNS(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != 3 {
		t.Fatalf("total, err = %d, %v; want 3 (topic, application, number), nil", total, err)
	}
	for typ, id := range map[string]string{TypeSNSTopic: snsTopicARN, TypeSNSPlatformApplication: snsAppARN1, TypeSNSOriginationNumber: snsPhoneID(snsPhone1)} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s row %s missing", typ, id)
		}
	}
}

// A hard failure of either new phase fails the service but keeps the rows of
// the topic phases and of the other new phase.
func TestScanSNS_NewPhaseFailureKeepsOtherPhases(t *testing.T) {
	hard := apiErr("InvalidParameter", "bad")
	for _, failing := range []string{"ListPlatformApplications", "ListOriginationNumbers"} {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			h := snsMerge(snsTopicHandlers(nil),
				snsAppHandler([][]snstypes.PlatformApplication{{snsApp(snsAppARN1)}}, nil),
				snsPhoneHandler([][]snstypes.PhoneNumberInformation{{snsPhone(snsPhone1)}}, nil, nil))
			h[failing] = func(any) (any, error) { return nil, hard }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, snsDenied)}

			total, _, err := scanSNS(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InvalidParameter") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v; want a hard InvalidParameter", err)
			}
			if total != 2 || len(rowsByNativeID(t, st, TypeSNSTopic)) != 1 {
				t.Errorf("total = %d; want 2 (the topic and the other new phase's row)", total)
			}
		})
	}
}

// A hard topic failure fails the service but the new phases still run.
func TestScanSNS_TopicFailureStillRunsNewPhases(t *testing.T) {
	st := newTestStore(t)
	h := snsMerge(snsTopicHandlers(apiErr("InvalidParameter", "bad")),
		snsAppHandler([][]snstypes.PlatformApplication{{snsApp(snsAppARN1)}}, nil),
		snsPhoneHandler([][]snstypes.PhoneNumberInformation{{snsPhone(snsPhone1)}}, nil, nil))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, snsDenied)}

	total, _, err := scanSNS(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidParameter") || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v; want a hard InvalidParameter", err)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeSNSPlatformApplication)) != 1 || len(rowsByNativeID(t, st, TypeSNSOriginationNumber)) != 1 {
		t.Errorf("total = %d; want 2 (the application and the number)", total)
	}
}

// A transient error in any phase is demoted to one warning, so a hard error
// in another phase still classifies the service as failed.
func TestScanSNS_TransientDemotedBesideHardError(t *testing.T) {
	transient := &smithyhttp.RequestSendError{Err: io.EOF}
	hard := apiErr("InvalidParameter", "bad")
	for _, tc := range []struct {
		name, transientOp, warnOp, hardOp string
	}{
		{"topics transient", "ListTopics", "sns", "ListOriginationNumbers"},
		{"applications transient", "ListPlatformApplications", snsListPlatformApplicationsOp, "ListTopics"},
		{"numbers transient", "ListOriginationNumbers", snsListOriginationNumbersOp, "ListPlatformApplications"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.warnOp)
			h := snsMerge(snsTopicHandlers(nil),
				snsAppHandler([][]snstypes.PlatformApplication{{snsApp(snsAppARN1)}}, nil),
				snsPhoneHandler([][]snstypes.PhoneNumberInformation{{snsPhone(snsPhone1)}}, nil, nil))
			h[tc.transientOp] = func(any) (any, error) { return nil, transient }
			h[tc.hardOp] = func(any) (any, error) { return nil, hard }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, snsDenied)}

			_, _, err := scanSNS(context.Background(), acct, testRegion, st, testScanID)
			if classifyServiceError(err) != outcomeError || *warns != 1 {
				t.Errorf("outcome, warnings = %v, %d; want outcomeError, 1 (err %v)", classifyServiceError(err), *warns, err)
			}
		})
	}
}

// snsWarnings counts the scan warnings recorded per op.
func snsWarnings(st *store.Store) func() map[string]int {
	var (
		mu sync.Mutex
		n  = map[string]int{}
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		n[w.Service]++
		mu.Unlock()
	}
	return func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(n)
	}
}

// SNS's own denial code on the topic phases warns once per denied op, is not
// an error, and leaves every other phase's rows.
func TestScanSNS_TopicPhasesAuthorizationErrorWarns(t *testing.T) {
	sub := snstypes.Subscription{SubscriptionArn: sdkaws.String(snsTopicARN + ":sub-1"), TopicArn: sdkaws.String(snsTopicARN)}
	for _, tc := range []struct {
		name      string
		deny      string // op answered with AuthorizationError
		wantWarns map[string]int
		wantTypes map[string]int // stored rows per type
	}{
		{
			name: "ListTopics", deny: "ListTopics",
			wantWarns: map[string]int{"sns:ListTopics": 1, "sns:ListTopics(policies)": 1},
			wantTypes: map[string]int{TypeSNSTopic: 0, TypeSNSSubscription: 1, TypeSNSPlatformApplication: 1, TypeSNSOriginationNumber: 1},
		},
		{
			name: "GetTopicAttributes", deny: "GetTopicAttributes",
			wantWarns: map[string]int{"sns:GetTopicAttributes": 1, "sns:GetTopicAttributes(policy)": 1},
			wantTypes: map[string]int{TypeSNSTopic: 0, TypeSNSTopicPolicy: 0, TypeSNSSubscription: 1, TypeSNSPlatformApplication: 1},
		},
		{
			name: "ListSubscriptions", deny: "ListSubscriptions",
			wantWarns: map[string]int{"sns:ListSubscriptions": 1},
			wantTypes: map[string]int{TypeSNSTopic: 2, TypeSNSTopicPolicy: 2, TypeSNSSubscription: 0, TypeSNSPlatformApplication: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := snsWarnings(st)
			h := snsMerge(
				map[string]func(any) (any, error){
					"ListTopics": func(any) (any, error) {
						return &sns.ListTopicsOutput{Topics: []snstypes.Topic{{TopicArn: sdkaws.String(snsTopicARN)}, {TopicArn: sdkaws.String(snsTopicARN + "-2")}}}, nil
					},
					"GetTopicAttributes": func(in any) (any, error) {
						arn := sdkaws.ToString(in.(*sns.GetTopicAttributesInput).TopicArn)
						return &sns.GetTopicAttributesOutput{Attributes: map[string]string{"TopicArn": arn, "Policy": `{"Version":"2012-10-17"}`}}, nil
					},
					"ListSubscriptions": func(any) (any, error) {
						return &sns.ListSubscriptionsOutput{Subscriptions: []snstypes.Subscription{sub}}, nil
					},
				},
				snsAppHandler([][]snstypes.PlatformApplication{{snsApp(snsAppARN1)}}, nil),
				snsPhoneHandler([][]snstypes.PhoneNumberInformation{{snsPhone(snsPhone1)}}, nil, nil))
			h[tc.deny] = func(any) (any, error) { return nil, snsDenied }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, snsDenied)}

			if _, _, err := scanSNS(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := warns(); !maps.Equal(got, tc.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, tc.wantWarns)
			}
			for typ, want := range tc.wantTypes {
				if got := len(rowsByNativeID(t, st, typ)); got != want {
					t.Errorf("%s rows = %d; want %d", typ, got, want)
				}
			}
		})
	}
}
