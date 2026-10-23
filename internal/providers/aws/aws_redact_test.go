package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	codebuildtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	lightsailtypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	mltypes "github.com/aws/aws-sdk-go-v2/service/machinelearning/types"
	mptypes "github.com/aws/aws-sdk-go-v2/service/mediapackage/types"
	mttypes "github.com/aws/aws-sdk-go-v2/service/mediatailor/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/icearp/disco-cli/internal/redact"
)

// applyAndDecode marshals v as the scanner would, runs redact.Apply for the
// type, then unmarshals into a generic map for assertion.
func applyAndDecode(t *testing.T, resourceType string, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := redact.Apply(resourceType, string(raw))
	if err != nil {
		t.Fatalf("redact.Apply(%q): %v", resourceType, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal redacted: %v\n%s", err, out)
	}
	return got
}

func TestRedact_LambdaFunction_EnvVariables(t *testing.T) {
	fc := lambdatypes.FunctionConfiguration{
		FunctionName: ptrStr("my-fn"),
		Environment: &lambdatypes.EnvironmentResponse{
			Variables: map[string]string{
				"DB_PASS":   "hunter2",
				"LOG_LEVEL": "debug",
			},
		},
	}
	got := applyAndDecode(t, TypeLambdaFunction, fc)
	if got["FunctionName"] != "my-fn" {
		t.Errorf("FunctionName clobbered: %v", got["FunctionName"])
	}
	vars := got["Environment"].(map[string]any)["Variables"].(map[string]any)
	for _, k := range []string{"DB_PASS", "LOG_LEVEL"} {
		if vars[k] != redact.Placeholder {
			t.Errorf("Variables[%s] not redacted: %v", k, vars[k])
		}
	}
}

// EC2 Instance.UserData and KeyPairInfo.KeyMaterial don't exist on the typed
// list-shape responses (they're DescribeInstanceAttribute / CreateKeyPair output
// fields). Tests round-trip a synthetic shape — the rule still binds to the JSON
// path, matching what scanners would emit if they ever fetched those fields.
func TestRedact_EC2Instance_UserData(t *testing.T) {
	got := applyAndDecode(t, TypeEC2Instance, map[string]any{
		"InstanceId": "i-abc",
		"UserData":   "#!/bin/bash\nexport DB=hunter2",
	})
	if got["UserData"] != redact.Placeholder {
		t.Errorf("UserData not redacted: %v", got["UserData"])
	}
	if got["InstanceId"] != "i-abc" {
		t.Errorf("InstanceId clobbered")
	}
}

func TestRedact_EC2IpamVerificationToken_TokenValue(t *testing.T) {
	got := applyAndDecode(t, TypeEC2IpamExternalResourceVerificationToken, map[string]any{
		"IpamId":     "ipam-aaa",
		"TokenName":  "my-token",
		"TokenValue": "super-secret-proof",
	})
	if got["TokenValue"] != redact.Placeholder {
		t.Errorf("TokenValue not redacted: %v", got["TokenValue"])
	}
	if got["TokenName"] != "my-token" {
		t.Errorf("TokenName clobbered: %v", got["TokenName"])
	}
}

func TestRedact_EC2KeyPair_KeyMaterial(t *testing.T) {
	got := applyAndDecode(t, TypeEC2KeyPair, map[string]any{
		"KeyName":     "k1",
		"KeyMaterial": "-----BEGIN PRIVATE KEY-----\nabc\n-----END",
	})
	if got["KeyMaterial"] != redact.Placeholder {
		t.Errorf("KeyMaterial not redacted: %v", got["KeyMaterial"])
	}
}

func TestRedact_SecretsManagerSecret_SecretString(t *testing.T) {
	// DescribeSecret response shape; SecretString only appears on
	// GetSecretValue (disco doesn't call it), but defensively redact.
	in := map[string]any{
		"Name":         "foo",
		"ARN":          "arn:aws:secretsmanager:us-east-1:123:secret:foo",
		"SecretString": "hunter2",
	}
	got := applyAndDecode(t, TypeSecretsManagerSecret, in)
	if got["SecretString"] != redact.Placeholder {
		t.Errorf("SecretString not redacted")
	}
	// ARN preserved (no rule on it).
	if got["ARN"] != in["ARN"] {
		t.Errorf("ARN clobbered: %v", got["ARN"])
	}
}

func TestRedact_RDSDBInstance_MasterUserPassword(t *testing.T) {
	db := rdstypes.DBInstance{
		DBInstanceIdentifier: ptrStr("db1"),
		MasterUsername:       ptrStr("admin"),
	}
	// Instance struct exposes no MasterUserPassword field on the read
	// response shape; inject via map round-trip to mirror a Create surface.
	raw, _ := json.Marshal(db)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["MasterUserPassword"] = "hunter2"
	got := applyAndDecode(t, TypeRDSDBInstance, m)
	if got["MasterUserPassword"] != redact.Placeholder {
		t.Errorf("MasterUserPassword not redacted")
	}
	if got["MasterUsername"] != "admin" {
		t.Errorf("MasterUsername clobbered: %v", got["MasterUsername"])
	}
}

func TestRedact_IAMAccessKey_SecretAccessKey(t *testing.T) {
	ak := iamtypes.AccessKey{
		AccessKeyId:     ptrStr("AKIATEST"),
		SecretAccessKey: ptrStr("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		Status:          iamtypes.StatusTypeActive,
	}
	got := applyAndDecode(t, TypeIAMAccessKey, ak)
	if got["SecretAccessKey"] != redact.Placeholder {
		t.Errorf("SecretAccessKey not redacted")
	}
	if got["AccessKeyId"] != "AKIATEST" {
		t.Errorf("AccessKeyId must stay clear: %v", got["AccessKeyId"])
	}
}

func TestRedact_LightsailBucketAccessKey_SecretAccessKey(t *testing.T) {
	ak := lightsailtypes.AccessKey{
		AccessKeyId:     ptrStr("AKIATEST"),
		SecretAccessKey: ptrStr("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		Status:          lightsailtypes.StatusTypeActive,
	}
	got := applyAndDecode(t, TypeLightsailBucketAccessKey, ak)
	if got["SecretAccessKey"] != redact.Placeholder {
		t.Errorf("SecretAccessKey = %v; want %q", got["SecretAccessKey"], redact.Placeholder)
	}
	if got["AccessKeyId"] != "AKIATEST" {
		t.Errorf("AccessKeyId must stay clear: %v", got["AccessKeyId"])
	}
}

func TestRedact_CognitoAppClient_ClientSecret(t *testing.T) {
	c := cognitoidptypes.UserPoolClientType{
		ClientId:     ptrStr("4example0client0id"),
		ClientName:   ptrStr("web"),
		ClientSecret: ptrStr("example0secret0value0not0real"),
	}
	got := applyAndDecode(t, TypeCognitoAppClient, c)
	if got["ClientSecret"] != redact.Placeholder {
		t.Errorf("ClientSecret = %v; want %q", got["ClientSecret"], redact.Placeholder)
	}
	if got["ClientId"] != "4example0client0id" || got["ClientName"] != "web" {
		t.Errorf("ClientId / ClientName must stay clear: %v / %v", got["ClientId"], got["ClientName"])
	}
}

func TestRedact_CognitoUserPoolClientSecret_ClientSecretValue(t *testing.T) {
	s := cognitoidptypes.ClientSecretDescriptorType{
		ClientSecretId:    ptrStr("client--1700000000"),
		ClientSecretValue: ptrStr("example0secret0value0not0real"),
	}
	got := applyAndDecode(t, TypeCognitoUserPoolClientSecret, s)
	if got["ClientSecretValue"] != redact.Placeholder {
		t.Errorf("ClientSecretValue = %v; want %q", got["ClientSecretValue"], redact.Placeholder)
	}
	if got["ClientSecretId"] != "client--1700000000" {
		t.Errorf("ClientSecretId must stay clear: %v", got["ClientSecretId"])
	}
}

func TestRedact_CodeBuildProject_PlaintextEnv(t *testing.T) {
	proj := codebuildtypes.Project{
		Name: ptrStr("p1"),
		Environment: &codebuildtypes.ProjectEnvironment{
			EnvironmentVariables: []codebuildtypes.EnvironmentVariable{
				{Name: ptrStr("DB_URL"), Value: ptrStr("postgres://x"), Type: codebuildtypes.EnvironmentVariableTypePlaintext},
				{Name: ptrStr("API_REF"), Value: ptrStr("arn:aws:secretsmanager:..."), Type: codebuildtypes.EnvironmentVariableTypeSecretsManager},
			},
		},
	}
	got := applyAndDecode(t, TypeCodeBuildProject, proj)
	envs := got["Environment"].(map[string]any)["EnvironmentVariables"].([]any)
	for _, e := range envs {
		em := e.(map[string]any)
		if em["Value"] != redact.Placeholder {
			t.Errorf("Value not redacted: %v", em)
		}
		// Names preserved.
		if em["Name"] == "" {
			t.Errorf("Name clobbered")
		}
	}
}

func TestRedact_DeviceFarmProject_EnvVariables(t *testing.T) {
	proj := dftypes.Project{
		Name: ptrStr("p1"),
		Arn:  ptrStr("arn:aws:devicefarm:us-west-2:111122223333:project:PROJ-1"),
		EnvironmentVariables: []dftypes.EnvironmentVariable{
			{Name: ptrStr("API_KEY"), Value: ptrStr("super-secret")},
			{Name: ptrStr("TOKEN"), Value: ptrStr("another-secret")},
		},
	}
	got := applyAndDecode(t, TypeDeviceFarmProject, proj)
	envs := got["EnvironmentVariables"].([]any)
	for _, e := range envs {
		em := e.(map[string]any)
		if em["Value"] != redact.Placeholder {
			t.Errorf("Value not redacted: %v", em)
		}
		if em["Name"] == "" {
			t.Errorf("Name clobbered")
		}
	}
}

// The URL, every header value and the body of an HTTP_REQUEST function are
// redacted; header names and the output bindings are kept.
func TestRedact_MediaTailorFunction_HTTPRequest(t *testing.T) {
	fn := mttypes.Function{
		FunctionId:   ptrStr("enrich"),
		FunctionType: mttypes.FunctionTypeHttpRequest,
		HttpRequestConfiguration: &mttypes.HttpRequestConfiguration{
			MethodType: mttypes.MethodTypePost,
			Runtime:    mttypes.RuntimeTypeJsonata,
			Url:        ptrStr("https://ads.example.com/segment"),
			Headers:    map[string]string{"Authorization": "Bearer sk-live-123", "Content-Type": "application/json"},
			Body:       ptrStr(`{"apiKey":"k-123"}`),
			Output:     map[string]string{"temp.segment": "response.body.segment"},
		},
	}
	got := applyAndDecode(t, TypeMediaTailorFunction, fn)
	hrc := got["HttpRequestConfiguration"].(map[string]any)
	headers := hrc["Headers"].(map[string]any)
	if len(headers) != 2 {
		t.Errorf("Headers = %v, want both names kept", headers)
	}
	for _, k := range []string{"Authorization", "Content-Type"} {
		if headers[k] != redact.Placeholder {
			t.Errorf("Headers[%s] = %v, want redacted", k, headers[k])
		}
	}
	if hrc["Body"] != redact.Placeholder {
		t.Errorf("Body = %v, want redacted", hrc["Body"])
	}
	if hrc["Url"] != redact.Placeholder {
		t.Errorf("Url = %v, want redacted", hrc["Url"])
	}
	if got["FunctionId"] != "enrich" {
		t.Errorf("FunctionId = %v, want kept", got["FunctionId"])
	}
	if out := hrc["Output"].(map[string]any); out["temp.segment"] != "response.body.segment" {
		t.Errorf("Output = %v, want kept", out)
	}
}

// Every ingest endpoint's password is redacted; its username and URL are
// kept.
func TestRedact_MediaPackageChannel_IngestPassword(t *testing.T) {
	ch := mptypes.Channel{
		Id: ptrStr("ch-1"),
		HlsIngest: &mptypes.HlsIngest{IngestEndpoints: []mptypes.IngestEndpoint{
			{Id: ptrStr("ie-1"), Password: ptrStr("pw-1"), Username: ptrStr("u-1"), Url: ptrStr("https://ingest.example/1")},
			{Id: ptrStr("ie-2"), Password: ptrStr("pw-2"), Username: ptrStr("u-2"), Url: ptrStr("https://ingest.example/2")},
		}},
	}
	got := applyAndDecode(t, TypeMediaPackageChannel, ch)
	eps := got["HlsIngest"].(map[string]any)["IngestEndpoints"].([]any)
	for i, e := range eps {
		em := e.(map[string]any)
		if em["Password"] != redact.Placeholder {
			t.Errorf("IngestEndpoints[%d].Password = %v, want redacted", i, em["Password"])
		}
		if want := fmt.Sprintf("u-%d", i+1); em["Username"] != want {
			t.Errorf("IngestEndpoints[%d].Username = %v, want %s kept", i, em["Username"], want)
		}
		if want := fmt.Sprintf("https://ingest.example/%d", i+1); em["Url"] != want {
			t.Errorf("IngestEndpoints[%d].Url = %v, want %s kept", i, em["Url"], want)
		}
	}
}

func TestRedact_AmplifyWebhook_WebhookUrl(t *testing.T) {
	wh := amplifytypes.Webhook{
		WebhookId:  ptrStr("wh-1"),
		WebhookArn: ptrStr("arn:aws:amplify:us-east-1:111122223333:apps/a/webhooks/wh-1"),
		BranchName: ptrStr("main"),
		WebhookUrl: ptrStr("https://webhooks.amplify.us-east-1.amazonaws.com/prod/webhooks?id=wh-1&token=SECRETTOKEN"),
	}
	got := applyAndDecode(t, TypeAmplifyWebhooks, wh)
	if got["WebhookUrl"] != redact.Placeholder {
		t.Errorf("WebhookUrl not redacted: %v", got["WebhookUrl"])
	}
	if got["BranchName"] != "main" {
		t.Errorf("BranchName must stay clear: %v", got["BranchName"])
	}
}

func TestRedact_CloudHSMCluster_PreCoPassword(t *testing.T) {
	c := cloudhsmv2types.Cluster{
		ClusterId:     ptrStr("cluster-abc"),
		PreCoPassword: ptrStr("super-secret"),
	}
	got := applyAndDecode(t, TypeCloudHSMCluster, c)
	if got["ClusterId"] != "cluster-abc" {
		t.Errorf("ClusterId clobbered: %v", got["ClusterId"])
	}
	if got["PreCoPassword"] != redact.Placeholder {
		t.Errorf("PreCoPassword not redacted: %v", got["PreCoPassword"])
	}
}

// TestRedact_UnruledTypePassesThrough pins that rules are never inferred: a
// type with no registered rule comes back unchanged, even a key named
// Password.
func TestRedact_UnruledTypePassesThrough(t *testing.T) {
	in := `{"Password":"x"}`
	out, err := redact.Apply("aws:fake:type", in)
	if err != nil || out != in {
		t.Errorf("redact.Apply(unruled, %s) = %s, %v; want input unchanged, nil", in, out, err)
	}
}

func TestRedact_DSSharedDirectory_ShareNotes(t *testing.T) {
	s := dstypes.SharedDirectory{
		SharedDirectoryId: ptrStr("d-consumer"),
		SharedAccountId:   ptrStr("210987654321"),
		ShareNotes:        ptrStr("join with the temporary password Hunter2"),
	}
	got := applyAndDecode(t, TypeDSSharedDirectory, s)
	if got["ShareNotes"] != redact.Placeholder {
		t.Errorf("ShareNotes not redacted: %v", got["ShareNotes"])
	}
	if got["SharedAccountId"] != "210987654321" || got["SharedDirectoryId"] != "d-consumer" {
		t.Errorf("ids clobbered: %v", got)
	}
}

func TestRedact_DSDirectory_ShareNotesAndRadiusSecret(t *testing.T) {
	d := dstypes.DirectoryDescription{
		DirectoryId:    ptrStr("d-1"),
		ShareNotes:     ptrStr("join with the temporary password Hunter2"),
		RadiusSettings: &dstypes.RadiusSettings{SharedSecret: ptrStr("radius-secret"), RadiusServers: []string{"10.0.0.5"}},
	}
	for _, typ := range []string{TypeDSMicrosoftAD, TypeDSSimpleAD} {
		t.Run(typ, func(t *testing.T) {
			got := applyAndDecode(t, typ, d)
			if got["ShareNotes"] != redact.Placeholder {
				t.Errorf("ShareNotes not redacted: %v", got["ShareNotes"])
			}
			radius, _ := got["RadiusSettings"].(map[string]any)
			if radius["SharedSecret"] != redact.Placeholder {
				t.Errorf("RadiusSettings.SharedSecret not redacted: %v", radius["SharedSecret"])
			}
			if servers, _ := radius["RadiusServers"].([]any); len(servers) != 1 || servers[0] != "10.0.0.5" || got["DirectoryId"] != "d-1" {
				t.Errorf("non-secret fields clobbered: %v", got)
			}
		})
	}
}

func ptrStr(s string) *string { return &s }

func TestRedact_ConnectTestCase_InitializationData(t *testing.T) {
	tc := cttypes.TestCase{
		Name:               ptrStr("smoke"),
		Content:            ptrStr(`{"Version":"2019-10-30"}`),
		InitializationData: ptrStr(`{"Attributes":{"CustomerPhone":"+15555550100","AuthToken":"eyJhbGciOi"}}`),
	}
	got := applyAndDecode(t, TypeConnectTestCase, tc)
	if got["InitializationData"] != redact.Placeholder {
		t.Errorf("InitializationData not redacted: %v", got["InitializationData"])
	}
	if got["Name"] != "smoke" || got["Content"] != `{"Version":"2019-10-30"}` {
		t.Errorf("siblings clobbered: Name=%v Content=%v", got["Name"], got["Content"])
	}
}

// An Amazon ML data source element carries no password (that exists only on
// the Create inputs); the database user name, query and IAM user are not
// credentials and are kept.
func TestRedact_MachineLearningDataSource_KeepsNonCredentials(t *testing.T) {
	d := mltypes.DataSource{
		DataSourceId:     ptrStr("ds-1"),
		CreatedByIamUser: ptrStr("arn:aws:iam::123456789012:user/alice"),
		RDSMetadata: &mltypes.RDSMetadata{
			DatabaseUserName: ptrStr("ml_reader"),
			SelectSqlQuery:   ptrStr("SELECT * FROM sales"),
		},
		RedshiftMetadata: &mltypes.RedshiftMetadata{DatabaseUserName: ptrStr("rs_reader")},
	}
	got := applyAndDecode(t, TypeMachineLearningDataSource, d)
	if got["CreatedByIamUser"] != "arn:aws:iam::123456789012:user/alice" {
		t.Errorf("CreatedByIamUser = %v, want kept", got["CreatedByIamUser"])
	}
	rds, _ := got["RDSMetadata"].(map[string]any)
	if rds["DatabaseUserName"] != "ml_reader" || rds["SelectSqlQuery"] != "SELECT * FROM sales" {
		t.Errorf("RDSMetadata = %v, want user name and query kept", rds)
	}
	rs, _ := got["RedshiftMetadata"].(map[string]any)
	if rs["DatabaseUserName"] != "rs_reader" {
		t.Errorf("RedshiftMetadata = %v, want user name kept", rs)
	}
}
