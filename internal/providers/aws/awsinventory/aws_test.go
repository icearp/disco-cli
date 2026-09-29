package awsinventory

import "testing"

func TestExpandServiceReference(t *testing.T) {
	entries, err := expandServiceReference([]byte(`[{"service":"ec2","url":"https://x/v1/ec2/ec2.json"},{"service":"","url":"https://x/none"},{"service":"s3","url":""}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "ec2" {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := expandServiceReference([]byte(`{`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSmithyKeep(t *testing.T) {
	keep := (extractor{}).FetchSpec()[0].Keep
	if !keep("codegen/sdk-codegen/aws-models/ec2.json") {
		t.Error("model rejected")
	}
	if keep("codegen/sdk-codegen/aws-models/README.md") || keep("service/ec2/api_client.go") {
		t.Error("non-model kept")
	}
}
