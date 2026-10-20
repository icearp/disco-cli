package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/accountaccess"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	"github.com/aws/aws-sdk-go-v2/service/keyspacesstreams"
	"github.com/aws/aws-sdk-go-v2/service/lambdacore"
	"github.com/aws/aws-sdk-go-v2/service/odb"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	smithycbor "github.com/aws/smithy-go/encoding/cbor"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestWithBothMessageKeys(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"capitalised only", `{"Message":"m","x":1}`, `{"message":"m","Message":"m","x":1}`},
		{"lowercase only", ` {"message":"m"}`, ` {"Message":"m","message":"m"}`},
		{"null value is copied raw", `{"Message":null}`, `{"message":null,"Message":null}`},
		{"both present", `{"message":"a","Message":"b"}`, `{"message":"a","Message":"b"}`},
		{"neither present", `{"__type":"X"}`, `{"__type":"X"}`},
		{"other casing ignored", `{"MESSAGE":"m"}`, `{"MESSAGE":"m"}`},
		{"empty object", `{}`, `{}`},
		{"json array", `[{"Message":"m"}]`, `[{"Message":"m"}]`},
		{"xml", `<Error><Message>m</Message></Error>`, `<Error><Message>m</Message></Error>`},
		{"empty", ``, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(withBothMessageKeys([]byte(tc.body))); got != tc.want {
				t.Errorf("withBothMessageKeys(%s) = %s, want %s", tc.body, got, tc.want)
			}
		})
	}
}

// TestWithBothMessageKeys_KeepsOtherBytes pins that the splice leaves every
// other byte alone; a re-marshal would reorder keys and reformat numbers.
func TestWithBothMessageKeys_KeepsOtherBytes(t *testing.T) {
	body := `{"z":1.50,"Message":"m","a":[1,2]}`
	got := withBothMessageKeys([]byte(body))
	if want := `{"message":"m","z":1.50,"Message":"m","a":[1,2]}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("result is not valid JSON: %s", got)
	}
}

// TestErrorMessageKeyRepair_HandleDeserialize covers which responses the repair
// touches, and that a body it declines to repair reaches the deserializer as it
// arrived.
func TestErrorMessageKeyRepair_HandleDeserialize(t *testing.T) {
	handle := func(t *testing.T, next stubDeserializeHandler) middleware.DeserializeOutput {
		t.Helper()
		out, _, err := errorMessageKeyRepair{}.HandleDeserialize(context.Background(), middleware.DeserializeInput{}, next)
		if err != nil {
			t.Fatalf("HandleDeserialize: %v", err)
		}
		return out
	}
	readBody := func(t *testing.T, out middleware.DeserializeOutput) string {
		t.Helper()
		resp, ok := out.RawResponse.(*smithyhttp.Response)
		if !ok || resp.Body == nil {
			t.Fatalf("no response body on output %#v", out.RawResponse)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return string(b)
	}

	t.Run("error response is repaired", func(t *testing.T) {
		out := handle(t, stubDeserializeHandler{status: 403, body: `{"Message":"m"}`})
		if got, want := readBody(t, out), `{"message":"m","Message":"m"}`; got != want {
			t.Errorf("body = %s, want %s", got, want)
		}
	})

	t.Run("success response is left byte-identical", func(t *testing.T) {
		// A 2xx body is the operation's payload, and an output shape may have a
		// member named Message.
		const payload = `{"Message":"m"}`
		out := handle(t, stubDeserializeHandler{status: 200, body: payload})
		if got := readBody(t, out); got != payload {
			t.Errorf("body = %s, want it untouched", got)
		}
	})

	t.Run("oversized body is passed through whole", func(t *testing.T) {
		big := `{"Message":"` + strings.Repeat("x", maxErrorBodyBytes) + `"}`
		out := handle(t, stubDeserializeHandler{status: 403, body: big})
		if got := readBody(t, out); got != big {
			t.Errorf("oversized body was altered: got %d bytes, want the original %d", len(got), len(big))
		}
	})

	t.Run("body read error is replayed after the bytes already read", func(t *testing.T) {
		const head = `{"Message":"par`
		readErr := errors.New("connection reset by peer")
		out := handle(t, readFailure(head, readErr))
		assertReadErrorReplayed(t, out, head, readErr)
	})

	t.Run("transport error passes through", func(t *testing.T) {
		sentinel := errors.New("dial tcp: connection refused")
		_, _, err := errorMessageKeyRepair{}.HandleDeserialize(context.Background(),
			middleware.DeserializeInput{}, stubDeserializeHandler{err: sentinel})
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want the transport error unchanged", err)
		}
	})
}

// accountConfigForTest builds the config loadAccounts builds, isolated from the
// developer's AWS environment, so a client made from it carries the same
// middleware a scan's clients do.
func accountConfigForTest(t *testing.T) sdkaws.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", testRegion)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ENDPOINT_URL", "")
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), baseLoadOptions("")...)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	cfg.Credentials = sdkaws.AnonymousCredentials{}
	cfg.RetryMaxAttempts = 1
	return cfg
}

// errBody is one error response: the code AWS names (empty for none), the
// message and the key it travels under (empty for no message member).
type errBody struct {
	status           int
	code, key, value string
}

func writeJSONError(w http.ResponseWriter, b errBody) {
	w.Header().Set("Content-Type", "application/json")
	if b.code != "" {
		w.Header().Set("X-Amzn-ErrorType", b.code)
	}
	w.WriteHeader(b.status)
	body := map[string]string{}
	if b.key != "" {
		body[b.key] = b.value
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeCBORError(w http.ResponseWriter, b errBody) {
	w.Header().Set("Content-Type", "application/cbor")
	w.Header().Set("Smithy-Protocol", "rpc-v2-cbor")
	w.WriteHeader(b.status)
	body := smithycbor.Map{}
	if b.code != "" {
		body["__type"] = smithycbor.String(b.code)
	}
	if b.key != "" {
		body[b.key] = smithycbor.String(b.value)
	}
	_, _ = w.Write(smithycbor.Encode(body))
}

func writeQueryError(w http.ResponseWriter, b errBody) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(b.status)
	code, msg := "", ""
	if b.code != "" {
		code = "<Code>" + b.code + "</Code>"
	}
	if b.key != "" {
		msg = "<" + b.key + ">" + b.value + "</" + b.key + ">"
	}
	_, _ = fmt.Fprintf(w, `<ErrorResponse><Error><Type>Sender</Type>%s%s</Error><RequestId>r</RequestId></ErrorResponse>`, code, msg)
}

// protocolFamily is one real client answering through one protocol's
// deserializer; the name says whether the module uses schema-based
// (de)serialization. keys are the message keys this wire format can carry.
// deniedUnmodeled marks a module whose model declares no error for deniedCode.
type protocolFamily struct {
	name            string
	deniedCode      string
	deniedUnmodeled bool
	keys            []string
	write           func(http.ResponseWriter, errBody)
	call            func(cfg sdkaws.Config, url string) error
}

var protocolFamilies = []protocolFamily{
	{
		name: "restJson1 schema-serde (accountaccess)", deniedCode: "AccessDeniedException",
		keys: []string{"message", "Message"}, write: writeJSONError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := accountaccess.NewFromConfig(cfg, func(o *accountaccess.Options) { o.BaseEndpoint = &url }).
				ListApplications(context.Background(), &accountaccess.ListApplicationsInput{})
			return err
		},
	},
	{
		name: "restJson1 schema-serde (sesv2)", deniedCode: "AccessDeniedException", deniedUnmodeled: true,
		keys: []string{"message", "Message"}, write: writeJSONError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := sesv2.NewFromConfig(cfg, func(o *sesv2.Options) { o.BaseEndpoint = &url }).
				ListEmailIdentities(context.Background(), &sesv2.ListEmailIdentitiesInput{})
			return err
		},
	},
	{
		name: "awsJson1_0 schema-serde (keyspacesstreams)", deniedCode: "AccessDeniedException",
		keys: []string{"message", "Message"}, write: writeJSONError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := keyspacesstreams.NewFromConfig(cfg, func(o *keyspacesstreams.Options) { o.BaseEndpoint = &url }).
				ListStreams(context.Background(), &keyspacesstreams.ListStreamsInput{})
			return err
		},
	},
	{
		name: "awsJson1_0 schema-serde (odb)", deniedCode: "AccessDeniedException",
		keys: []string{"message", "Message"}, write: writeJSONError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := odb.NewFromConfig(cfg, func(o *odb.Options) { o.BaseEndpoint = &url }).
				ListCloudExadataInfrastructures(context.Background(), &odb.ListCloudExadataInfrastructuresInput{})
			return err
		},
	},
	{
		name: "restJson1 pre-serde (backup)", deniedCode: "AccessDeniedException", deniedUnmodeled: true,
		keys: []string{"message", "Message"}, write: writeJSONError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := backup.NewFromConfig(cfg, func(o *backup.Options) { o.BaseEndpoint = &url }).
				ListBackupVaults(context.Background(), &backup.ListBackupVaultsInput{})
			return err
		},
	},
	{
		name: "awsJson1_0 pre-serde (keyspaces)", deniedCode: "AccessDeniedException",
		keys: []string{"message", "Message"}, write: writeJSONError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := keyspaces.NewFromConfig(cfg, func(o *keyspaces.Options) { o.BaseEndpoint = &url }).
				ListKeyspaces(context.Background(), &keyspaces.ListKeyspacesInput{})
			return err
		},
	},
	{
		// A CBOR body is written by the service from its model, so only the
		// modeled key occurs; the repair must pass it through untouched.
		name: "rpcv2Cbor schema-serde (computeoptimizer)", deniedCode: "AccessDeniedException",
		keys: []string{"message"}, write: writeCBORError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := computeoptimizer.NewFromConfig(cfg, func(o *computeoptimizer.Options) { o.BaseEndpoint = &url }).
				GetEC2InstanceRecommendations(context.Background(), &computeoptimizer.GetEC2InstanceRecommendationsInput{})
			return err
		},
	},
	{
		name: "awsQuery pre-serde (iam)", deniedCode: "AccessDenied", deniedUnmodeled: true,
		keys: []string{"Message"}, write: writeQueryError,
		call: func(cfg sdkaws.Config, url string) error {
			_, err := iam.NewFromConfig(cfg, func(o *iam.Options) { o.BaseEndpoint = &url }).
				ListRoles(context.Background(), &iam.ListRolesInput{})
			return err
		},
	},
}

// predicateVerdicts are the message-based predicates' answers for one error.
type predicateVerdicts struct {
	accessDenied, notInRegion, notAuthorizedForRegion, scpDeny, closed, blocked bool
}

func verdictsOf(err error) predicateVerdicts {
	return predicateVerdicts{
		accessDenied:           isAccessDenied(err),
		notInRegion:            isServiceNotAvailableInRegion(err),
		notAuthorizedForRegion: isNotAuthorizedForRegion(err),
		scpDeny:                isSCPExplicitDeny(err),
		closed:                 isClosedToNewCustomers(err),
		blocked:                isServiceBlocked(err),
	}
}

// TestErrorMessageKeyRepair_PredicatesPerProtocol runs each message-based
// predicate against a real client of every protocol family, with the message
// under every key that family can carry. A schema-serde module's modeled error
// reads only the key its model names, so without the repair the "Message" rows
// of the serde JSON families lose their message: the gap and SCP checks miss,
// and a real denial reads as closed to new customers.
func TestErrorMessageKeyRepair_PredicatesPerProtocol(t *testing.T) {
	const denial = "User: arn:aws:iam::123456789012:user/scanner is not authorized to perform: svc:ListThings"
	cases := []struct {
		name    string
		blocked bool // no error code, as the blocked-service response carries none
		message string
		want    predicateVerdicts
	}{
		{
			name: "gateway region gap", message: "Unable to determine service/operation name to be authorized",
			want: predicateVerdicts{accessDenied: true, notInRegion: true},
		},
		{
			name: "real IAM denial", message: denial,
			want: predicateVerdicts{accessDenied: true},
		},
		{
			name: "SCP denial", message: denial + " with an explicit deny in a service control policy",
			want: predicateVerdicts{accessDenied: true, scpDeny: true},
		},
		{
			name: "account not authorized for region", message: "Account: 123456789012 is not authorized for region: us-east-1",
			want: predicateVerdicts{accessDenied: true, notAuthorizedForRegion: true},
		},
		{
			name: "service blocked", blocked: true, message: "AWS Telco Network Builder is deprecated. All API operations are blocked.",
			want: predicateVerdicts{blocked: true},
		},
	}
	cfg := accountConfigForTest(t)
	for _, fam := range protocolFamilies {
		for _, tc := range cases {
			for _, key := range fam.keys {
				body := errBody{status: http.StatusForbidden, code: fam.deniedCode, key: key, value: tc.message}
				if tc.blocked {
					body.code = ""
				}
				t.Run(fam.name+"/"+tc.name+"/"+key, func(t *testing.T) {
					assertVerdicts(t, cfg, fam, body, tc.want)
				})
			}
		}
		// An unmodeled code with no message reaches the caller as a generic
		// error whose message is the protocol's "UnknownError" placeholder, so
		// isClosedToNewCustomers cannot see it as empty. That holds with or
		// without the repair, so it is out of this test's scope.
		if fam.deniedUnmodeled {
			continue
		}
		t.Run(fam.name+"/empty-message denial", func(t *testing.T) {
			body := errBody{status: http.StatusForbidden, code: fam.deniedCode}
			assertVerdicts(t, cfg, fam, body, predicateVerdicts{accessDenied: true, closed: true})
		})
	}
}

// TestErrorMessageKeyRepair_LowercaseKeyForCapitalisedMember covers the other
// direction: lambdacore models ResourceNotFoundException's member as "Message",
// so a body spelling it "message" loses the text without the repair.
func TestErrorMessageKeyRepair_LowercaseKeyForCapitalisedMember(t *testing.T) {
	const msg = "Unable to determine service/operation name to be authorized"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, errBody{status: http.StatusNotFound, code: "ResourceNotFoundException", key: "message", value: msg})
	}))
	defer srv.Close()
	cfg := accountConfigForTest(t)

	_, err := lambdacore.NewFromConfig(cfg, func(o *lambdacore.Options) { o.BaseEndpoint = &srv.URL }).
		ListNetworkConnectors(context.Background(), &lambdacore.ListNetworkConnectorsInput{})

	if !isAPIErrorWithMessage(err, "ResourceNotFoundException", msg) {
		t.Errorf("isAPIErrorWithMessage(ResourceNotFoundException, gateway text) = false for %v", err)
	}
}

func assertVerdicts(t *testing.T, cfg sdkaws.Config, fam protocolFamily, body errBody, want predicateVerdicts) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fam.write(w, body) }))
	defer srv.Close()

	err := fam.call(cfg, srv.URL)
	if err == nil {
		t.Fatal("call succeeded, want an error")
	}
	if got := verdictsOf(err); got != want {
		t.Errorf("verdicts = %+v, want %+v (err: %v)", got, want, err)
	}
}
