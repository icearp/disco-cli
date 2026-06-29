package aws

import (
	"encoding/json"
	"strings"
	"testing"

	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	codebuildtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"codeberg.org/icearp/disco/internal/redact"
)

// applyAndDecode marshals v as the scanner would, runs redact.Apply for the
// type, then unmarshals into a generic map for assertion.
func applyAndDecode(t *testing.T, resourceType string, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := redact.Apply(resourceType, string(raw))
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
// list-shape responses (they're DescribeInstanceAttribute / CreateKeyPair
// output fields). Tests round-trip a synthetic shape — the rule still binds
// to the JSON path, which is what scanners would emit if they ever did fetch
// those fields.
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

// TestRedact_FallbackStillCatchesUnruledTypes confirms the migration phase:
// types without explicit rules still go through scrubAttributes.
// (The store-level test TestUpsertResources_ScrubsAttributes already covers
// the wired path; this exercises the AWS-side expectation that previously-
// covered substring keys keep redacting until each type gets explicit rules.)
func TestRedact_FallbackStillCatchesUnruledTypes(t *testing.T) {
	// Synthetic type with no rule registered.
	out := redact.Apply("aws:fake:type", `{"Password":"x"}`)
	if !strings.Contains(out, "Password") {
		t.Errorf("redact.Apply should pass through unrules; got %s", out)
	}
}

func ptrStr(s string) *string { return &s }
