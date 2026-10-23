package aws

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"testing"

	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	appsynctypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	codebuildtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	emrtypes "github.com/aws/aws-sdk-go-v2/service/emr/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	lightsailtypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	mltypes "github.com/aws/aws-sdk-go-v2/service/machinelearning/types"
	mptypes "github.com/aws/aws-sdk-go-v2/service/mediapackage/types"
	mttypes "github.com/aws/aws-sdk-go-v2/service/mediatailor/types"
	pptypes "github.com/aws/aws-sdk-go-v2/service/pinpoint/types"
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

// amplifyEnv holds a secret under a secret-looking key, a plain setting, a
// boolean and an ARN reference.
func amplifyEnv() map[string]string {
	return map[string]string{
		"DB_PASSWORD": "hunter2",
		"STAGE":       "prod",
		"FEATURE_X":   "true",
		"TABLE_ARN":   "arn:aws:dynamodb:us-east-1:111122223333:table/t",
	}
}

// assertAmplifyEnvRedacted checks env, a decoded EnvironmentVariables map:
// the secret and the plain setting are replaced, the boolean and the ARN kept.
func assertAmplifyEnvRedacted(t *testing.T, where string, env any) {
	t.Helper()
	got, ok := env.(map[string]any)
	if !ok {
		t.Fatalf("%s EnvironmentVariables = %v, want an object", where, env)
	}
	want := map[string]any{
		"DB_PASSWORD": redact.PlaceholderKeyName,
		"STAGE":       redact.PlaceholderRule,
		"FEATURE_X":   "true",
		"TABLE_ARN":   "arn:aws:dynamodb:us-east-1:111122223333:table/t",
	}
	if !maps.Equal(got, want) {
		t.Errorf("%s EnvironmentVariables = %v, want %v", where, got, want)
	}
}

// An Amplify app's basic-auth credentials (base64 user:password), its own and
// those it gives auto-created branches, are redacted, and so are its
// environment variables' values except references and booleans.
func TestRedact_AmplifyApp_Credentials(t *testing.T) {
	app := amplifytypes.App{
		AppId:                ptrStr("d1"),
		Name:                 ptrStr("site"),
		BasicAuthCredentials: ptrStr("dXNlcjpwYXNz"),
		EnvironmentVariables: amplifyEnv(),
		AutoBranchCreationConfig: &amplifytypes.AutoBranchCreationConfig{
			BasicAuthCredentials: ptrStr("dXNlcjpwYXNz"),
			EnvironmentVariables: amplifyEnv(),
			Framework:            ptrStr("React"),
		},
	}
	got := applyAndDecode(t, TypeAmplifyApp, app)
	if got["BasicAuthCredentials"] != redact.Placeholder {
		t.Errorf("BasicAuthCredentials = %v, want redacted", got["BasicAuthCredentials"])
	}
	assertAmplifyEnvRedacted(t, "app", got["EnvironmentVariables"])
	auto := got["AutoBranchCreationConfig"].(map[string]any)
	if auto["BasicAuthCredentials"] != redact.Placeholder {
		t.Errorf("AutoBranchCreationConfig.BasicAuthCredentials = %v, want redacted", auto["BasicAuthCredentials"])
	}
	assertAmplifyEnvRedacted(t, "AutoBranchCreationConfig", auto["EnvironmentVariables"])
	if got["Name"] != "site" || auto["Framework"] != "React" {
		t.Errorf("Name, Framework = %v, %v; want site, React kept", got["Name"], auto["Framework"])
	}
}

// An Amplify branch's basic-auth credentials are redacted, and so are its
// environment variables' values except references and booleans.
func TestRedact_AmplifyBranch_Credentials(t *testing.T) {
	b := amplifytypes.Branch{
		BranchName:           ptrStr("main"),
		BasicAuthCredentials: ptrStr("dXNlcjpwYXNz"),
		EnvironmentVariables: amplifyEnv(),
	}
	got := applyAndDecode(t, TypeAmplifyBranch, b)
	if got["BasicAuthCredentials"] != redact.Placeholder {
		t.Errorf("BasicAuthCredentials = %v, want redacted", got["BasicAuthCredentials"])
	}
	assertAmplifyEnvRedacted(t, "branch", got["EnvironmentVariables"])
	if got["BranchName"] != "main" {
		t.Errorf("BranchName = %v, want main kept", got["BranchName"])
	}
}

// A branch's thumbnail URL, and the app's production-branch copy of it, are
// redacted: a presigned URL is a bearer link that changes on every read. The
// production branch's other fields are kept.
func TestRedact_Amplify_ThumbnailUrl(t *testing.T) {
	const thumb = "https://bucket.s3.amazonaws.com/thumb.png?X-Amz-Signature=abc"
	b := applyAndDecode(t, TypeAmplifyBranch, amplifytypes.Branch{BranchName: ptrStr("main"), ThumbnailUrl: ptrStr(thumb)})
	if b["ThumbnailUrl"] != redact.Placeholder {
		t.Errorf("branch ThumbnailUrl = %v, want redacted", b["ThumbnailUrl"])
	}
	app := applyAndDecode(t, TypeAmplifyApp, amplifytypes.App{
		Name:             ptrStr("site"),
		ProductionBranch: &amplifytypes.ProductionBranch{BranchName: ptrStr("main"), ThumbnailUrl: ptrStr(thumb)},
	})
	pb := app["ProductionBranch"].(map[string]any)
	if pb["ThumbnailUrl"] != redact.Placeholder || pb["BranchName"] != "main" {
		t.Errorf("ProductionBranch = %v, want ThumbnailUrl redacted and BranchName main kept", pb)
	}
}

// An AppSync API key's Id is the key itself.
func TestRedact_AppSyncAPIKey_Id(t *testing.T) {
	k := appsynctypes.ApiKey{Id: ptrStr("da2-secretkeyvalue"), Description: ptrStr("ci key"), Expires: 1700000000}
	got := applyAndDecode(t, TypeAppSyncAPIKey, k)
	if got["Id"] != redact.Placeholder {
		t.Errorf("Id not redacted: %v", got["Id"])
	}
	if got["Description"] != "ci key" {
		t.Errorf("Description must stay clear: %v", got["Description"])
	}
}

// The Baidu channel's Credential is its Baidu Cloud Push API key and the GCM
// channel's is its Google Web API key.
func TestRedact_PinpointChannels_Credential(t *testing.T) {
	for typ, v := range map[string]any{
		TypePinpointBaiduChannel: pptypes.BaiduChannelResponse{Credential: ptrStr("baidu-api-key"), Platform: ptrStr("BAIDU")},
		TypePinpointGCMChannel:   pptypes.GCMChannelResponse{Credential: ptrStr("gcm-api-key"), Platform: ptrStr("GCM")},
	} {
		got := applyAndDecode(t, typ, v)
		if got["Credential"] != redact.Placeholder {
			t.Errorf("%s Credential not redacted: %v", typ, got["Credential"])
		}
		if got["Platform"] == nil || got["Platform"] == redact.Placeholder {
			t.Errorf("%s Platform must stay clear: %v", typ, got["Platform"])
		}
	}
}

// A campaign hook's WebUrl and a custom delivery URI that is a URL can carry a
// token; a DeliveryUri naming a Lambda function by ARN is kept, as is the
// hook's LambdaFunctionName.
func TestRedact_PinpointCampaign_WebhookURLs(t *testing.T) {
	const url = "https://hooks.example.com/deliver?token=abc123"
	const fn = "arn:aws:lambda:us-east-1:123456789012:function:deliver"
	c := pptypes.CampaignResponse{
		Hook:                        &pptypes.CampaignHook{WebUrl: ptrStr(url), LambdaFunctionName: ptrStr(fn)},
		CustomDeliveryConfiguration: &pptypes.CustomDeliveryConfiguration{DeliveryUri: ptrStr(url)},
		AdditionalTreatments: []pptypes.TreatmentResource{
			{CustomDeliveryConfiguration: &pptypes.CustomDeliveryConfiguration{DeliveryUri: ptrStr(url)}},
			{CustomDeliveryConfiguration: &pptypes.CustomDeliveryConfiguration{DeliveryUri: ptrStr(fn)}},
		},
	}
	got := applyAndDecode(t, TypePinpointCampaign, c)
	hook := got["Hook"].(map[string]any)
	if hook["WebUrl"] != redact.Placeholder {
		t.Errorf("Hook.WebUrl not redacted: %v", hook["WebUrl"])
	}
	if hook["LambdaFunctionName"] != fn {
		t.Errorf("Hook.LambdaFunctionName = %v, want %s kept", hook["LambdaFunctionName"], fn)
	}
	if uri := got["CustomDeliveryConfiguration"].(map[string]any)["DeliveryUri"]; !ppURIRedacted(uri) {
		t.Errorf("CustomDeliveryConfiguration.DeliveryUri not redacted: %v", uri)
	}
	treatments := got["AdditionalTreatments"].([]any)
	if uri := treatments[0].(map[string]any)["CustomDeliveryConfiguration"].(map[string]any)["DeliveryUri"]; !ppURIRedacted(uri) {
		t.Errorf("AdditionalTreatments[0] DeliveryUri not redacted: %v", uri)
	}
	if uri := treatments[1].(map[string]any)["CustomDeliveryConfiguration"].(map[string]any)["DeliveryUri"]; uri != fn {
		t.Errorf("AdditionalTreatments[1] DeliveryUri = %v, want the Lambda ARN %s kept", uri, fn)
	}
}

// ppURIRedacted reports whether a RedactUnlessSafe value came back as one of
// its reason-tagged placeholders.
func ppURIRedacted(v any) bool {
	return v == redact.PlaceholderRule || v == redact.PlaceholderValueShape || v == redact.PlaceholderKeyName
}

// A top-level DeliveryUri naming a Lambda function by ARN is kept.
func TestRedact_PinpointCampaign_DeliveryURIKeepsLambdaARN(t *testing.T) {
	const fn = "arn:aws:lambda:us-east-1:123456789012:function:deliver"
	got := applyAndDecode(t, TypePinpointCampaign, pptypes.CampaignResponse{
		CustomDeliveryConfiguration: &pptypes.CustomDeliveryConfiguration{DeliveryUri: ptrStr(fn)},
	})
	if uri := got["CustomDeliveryConfiguration"].(map[string]any)["DeliveryUri"]; uri != fn {
		t.Errorf("DeliveryUri = %v, want the Lambda ARN %s kept", uri, fn)
	}
}

// The application settings' campaign hook WebUrl can carry a token.
func TestRedact_PinpointApplicationSettings_CampaignHookWebUrl(t *testing.T) {
	const fn = "arn:aws:lambda:us-east-1:123456789012:function:hook"
	got := applyAndDecode(t, TypePinpointApplicationSettings, pptypes.ApplicationSettingsResource{
		CampaignHook: &pptypes.CampaignHook{WebUrl: ptrStr("https://hooks.example.com/x?sig=s3cr3t"), LambdaFunctionName: ptrStr(fn)},
	})
	hook := got["CampaignHook"].(map[string]any)
	if hook["WebUrl"] != redact.Placeholder {
		t.Errorf("CampaignHook.WebUrl not redacted: %v", hook["WebUrl"])
	}
	if hook["LambdaFunctionName"] != fn {
		t.Errorf("CampaignHook.LambdaFunctionName = %v, want %s kept", hook["LambdaFunctionName"], fn)
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

// emrSecretProps and emrPlainProps are application properties EMR
// configurations carry: the first hold credentials, the second settings.
var (
	emrSecretProps = map[string]string{
		"javax.jdo.option.ConnectionPassword": "hunter2",
		"fs.s3a.secret.key":                   "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		// Not shaped like an AWS key id, so only the key name can redact it.
		"fs.s3a.access.key": "example-access-key-id",
	}
	emrPlainProps = map[string]string{
		"hive.metastore.uris":   "thrift://metastore.internal:9083",
		"javax.jdo.option.User": "hive",
	}
)

func emrTestProps() map[string]string {
	props := maps.Clone(emrSecretProps)
	maps.Copy(props, emrPlainProps)
	return props
}

// emrTestConfigurations is a configuration whose properties and whose one
// nested configuration's properties both mix secrets and settings; the
// nested one holds a third-level configuration of the same kind.
func emrTestConfigurations() []emrtypes.Configuration {
	deep := []emrtypes.Configuration{{Classification: ptrStr("deep"), Properties: emrTestProps()}}
	return []emrtypes.Configuration{{
		Classification: ptrStr("hive-site"),
		Properties:     emrTestProps(),
		Configurations: []emrtypes.Configuration{{Classification: ptrStr("export"), Properties: emrTestProps(), Configurations: deep}},
	}}
}

// assertEMRConfigurationsRedacted fails unless, in configs (the decoded
// Configurations), every secret property of the top-level and nested
// configurations is redacted by key name and every setting is kept, and the
// third-level configurations, which no path judges, are redacted whole.
func assertEMRConfigurationsRedacted(t *testing.T, where string, configs any) {
	t.Helper()
	outer, _ := configs.([]any)
	if len(outer) != 1 {
		t.Fatalf("%s Configurations = %v; want one", where, configs)
	}
	top, _ := outer[0].(map[string]any)
	inner, _ := top["Configurations"].([]any)
	if len(inner) != 1 {
		t.Fatalf("%s nested Configurations = %v; want one", where, top["Configurations"])
	}
	nested, _ := inner[0].(map[string]any)
	for level, cfg := range map[string]map[string]any{"top": top, "nested": nested} {
		props, _ := cfg["Properties"].(map[string]any)
		for k := range emrSecretProps {
			if props[k] != redact.PlaceholderKeyName {
				t.Errorf("%s %s Properties[%s] = %v; want %s", where, level, k, props[k], redact.PlaceholderKeyName)
			}
		}
		for k, v := range emrPlainProps {
			if props[k] != v {
				t.Errorf("%s %s Properties[%s] = %v; want %q kept", where, level, k, props[k], v)
			}
		}
	}
	deepList, _ := nested["Configurations"].([]any)
	if len(deepList) != 1 {
		t.Fatalf("%s third-level Configurations = %v; want one", where, nested["Configurations"])
	}
	deep, _ := deepList[0].(map[string]any)
	if deep["Classification"] != redact.PlaceholderRule {
		t.Errorf("%s third-level Classification = %v; want %s", where, deep["Classification"], redact.PlaceholderRule)
	}
	props, _ := deep["Properties"].(map[string]any)
	if len(props) != len(emrTestProps()) {
		t.Fatalf("%s third-level Properties = %v; want every key", where, deep["Properties"])
	}
	for k, v := range props {
		if v != redact.PlaceholderRule {
			t.Errorf("%s third-level Properties[%s] = %v; want %s", where, k, v, redact.PlaceholderRule)
		}
	}
}

func TestRedact_EMRCluster_KerberosPasswordsAndConfigurations(t *testing.T) {
	got := applyAndDecode(t, TypeEMRCluster, emrtypes.Cluster{
		Id: ptrStr("j-1"),
		KerberosAttributes: &emrtypes.KerberosAttributes{
			Realm: ptrStr("EC2.INTERNAL"), ADDomainJoinUser: ptrStr("joiner"),
			KdcAdminPassword: ptrStr("kdc-secret"), ADDomainJoinPassword: ptrStr("ad-secret"),
			CrossRealmTrustPrincipalPassword: ptrStr("trust-secret"),
		},
		Configurations: emrTestConfigurations(),
	})
	if got["Id"] != "j-1" {
		t.Errorf("Id = %v; want kept", got["Id"])
	}
	kerberos, _ := got["KerberosAttributes"].(map[string]any)
	for _, k := range []string{"KdcAdminPassword", "ADDomainJoinPassword", "CrossRealmTrustPrincipalPassword"} {
		if kerberos[k] != redact.Placeholder {
			t.Errorf("KerberosAttributes.%s = %v; want %s", k, kerberos[k], redact.Placeholder)
		}
	}
	if kerberos["Realm"] != "EC2.INTERNAL" || kerberos["ADDomainJoinUser"] != "joiner" {
		t.Errorf("KerberosAttributes = %v; want Realm and ADDomainJoinUser kept", kerberos)
	}
	assertEMRConfigurationsRedacted(t, "cluster", got["Configurations"])
}

func TestRedact_EMRInstanceGroup_Configurations(t *testing.T) {
	got := applyAndDecode(t, TypeEMRInstanceGroup, emrtypes.InstanceGroup{
		Id:                                    ptrStr("ig-1"),
		Configurations:                        emrTestConfigurations(),
		LastSuccessfullyAppliedConfigurations: emrTestConfigurations(),
	})
	assertEMRConfigurationsRedacted(t, "instance group", got["Configurations"])
	assertEMRConfigurationsRedacted(t, "instance group last applied", got["LastSuccessfullyAppliedConfigurations"])
}

func TestRedact_EMRInstanceFleet_Configurations(t *testing.T) {
	got := applyAndDecode(t, TypeEMRInstanceFleet, emrtypes.InstanceFleet{
		Id: ptrStr("if-1"),
		InstanceTypeSpecifications: []emrtypes.InstanceTypeSpecification{
			{InstanceType: ptrStr("m5.xlarge"), Configurations: emrTestConfigurations()},
		},
	})
	specs, _ := got["InstanceTypeSpecifications"].([]any)
	if len(specs) != 1 {
		t.Fatalf("InstanceTypeSpecifications = %v; want one", got["InstanceTypeSpecifications"])
	}
	spec, _ := specs[0].(map[string]any)
	assertEMRConfigurationsRedacted(t, "instance fleet", spec["Configurations"])
}

func TestRedact_EMRStep_ConfigPropertiesAndArgs(t *testing.T) {
	args := []string{
		"spark.hadoop.fs.s3a.secret.key=abc",
		"-Djavax.jdo.option.ConnectionPassword=abc",
		"s3://bucket/script.py",
		"--deploy-mode",
	}
	got := applyAndDecode(t, TypeEMRStep, emrtypes.StepSummary{
		Id: ptrStr("s-1"),
		Config: &emrtypes.HadoopStepConfig{
			Jar:        ptrStr("command-runner.jar"),
			Args:       args,
			Properties: map[string]string{"fs.s3a.secret.key": "abc", "spark.executor.memory": "4g"},
		},
	})
	cfg, _ := got["Config"].(map[string]any)
	gotArgs, _ := cfg["Args"].([]any)
	want := []any{redact.PlaceholderValueShape, redact.PlaceholderValueShape, "s3://bucket/script.py", "--deploy-mode"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("Config.Args = %v; want %v", gotArgs, want)
	}
	props, _ := cfg["Properties"].(map[string]any)
	if props["fs.s3a.secret.key"] != redact.PlaceholderKeyName || props["spark.executor.memory"] != "4g" {
		t.Errorf("Config.Properties = %v; want the secret key redacted and spark.executor.memory kept", props)
	}
	if cfg["Jar"] != "command-runner.jar" {
		t.Errorf("Config.Jar = %v; want kept", cfg["Jar"])
	}
}
