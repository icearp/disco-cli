package aws

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

func sesDecode(t *testing.T, v any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(v)), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// Each SigningScheme member is stored under its own name, so the default
// scheme and an S/MIME scheme stay distinguishable.
func TestFlattenSESConfigurationSet_SigningSchemeMembers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme sesv2types.SigningScheme
		want   map[string]any
	}{
		{"default", &sesv2types.SigningSchemeMemberDefaultScheme{}, map[string]any{"DefaultScheme": map[string]any{}}},
		{
			"smime", &sesv2types.SigningSchemeMemberSmimeScheme{Value: sesv2types.SmimeSigningScheme{SignatureFormat: "DETACHED"}},
			map[string]any{"SmimeScheme": map[string]any{"SignatureFormat": "DETACHED"}},
		},
		{
			"unknown", &sesv2types.UnknownUnionMember{Tag: "FutureScheme", Value: []byte(`{}`)},
			map[string]any{"UnknownMember": map[string]any{"Tag": "FutureScheme", "Value": "e30="}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &sesv2.GetConfigurationSetOutput{
				ConfigurationSetName:   ptrStr("cs"),
				MessageSecurityOptions: &sesv2types.MessageSecurityOptions{SigningScheme: tc.scheme},
			}
			got := sesDecode(t, flattenSESConfigurationSet(out))
			if got["ConfigurationSetName"] != "cs" {
				t.Errorf("ConfigurationSetName = %v, want cs", got["ConfigurationSetName"])
			}
			mso, _ := got["MessageSecurityOptions"].(map[string]any)
			if !reflect.DeepEqual(mso["SigningScheme"], tc.want) {
				t.Errorf("SigningScheme = %v, want %v", mso["SigningScheme"], tc.want)
			}
		})
	}
}

// The configuration-set phase stores the shadow, not the raw SDK body.
func TestScanSESConfigurationSets_StoresKeyedSigningScheme(t *testing.T) {
	st := newTestStore(t)
	client := sesv2.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListConfigurationSets": func(any) (any, error) {
			return &sesv2.ListConfigurationSetsOutput{ConfigurationSets: []string{"cs"}}, nil
		},
		"GetConfigurationSet": func(any) (any, error) {
			return &sesv2.GetConfigurationSetOutput{
				ConfigurationSetName:   ptrStr("cs"),
				MessageSecurityOptions: &sesv2types.MessageSecurityOptions{SigningScheme: &sesv2types.SigningSchemeMemberDefaultScheme{}},
			}, nil
		},
	}, sesDenied))
	total, _, err := scanSESConfigurationSets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	r, ok := rowsByNativeID(t, st, TypeSESConfigurationSet)[sesConfigurationSetARN(testRegion, testAccountID, "cs")]
	if !ok {
		t.Fatal("configuration set row missing")
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	mso, _ := attrs["MessageSecurityOptions"].(map[string]any)
	if want := map[string]any{"DefaultScheme": map[string]any{}}; !reflect.DeepEqual(mso["SigningScheme"], want) {
		t.Errorf("SigningScheme = %v, want %v", mso["SigningScheme"], want)
	}
}

// A configuration set without MessageSecurityOptions stores no such key, the
// shape stored before the sesv2 module had the field, so the bump gives it no
// new version; every other key is the SDK's.
func TestFlattenSESConfigurationSet_NoMessageSecurityOptionsOmitted(t *testing.T) {
	out := &sesv2.GetConfigurationSetOutput{ConfigurationSetName: ptrStr("cs")}
	got := sesDecode(t, flattenSESConfigurationSet(out))
	if v, ok := got["MessageSecurityOptions"]; ok {
		t.Errorf("MessageSecurityOptions = %v, want the key absent", v)
	}
	want := sesDecode(t, out)
	delete(want, "MessageSecurityOptions")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flattened = %v, want SDK shape without MessageSecurityOptions %v", got, want)
	}
}

// MessageSecurityOptions without a SigningScheme stores what the embedded SDK
// struct does.
func TestFlattenSESConfigurationSet_NoSigningSchemeMatchesSDK(t *testing.T) {
	out := &sesv2.GetConfigurationSetOutput{ConfigurationSetName: ptrStr("cs"), MessageSecurityOptions: &sesv2types.MessageSecurityOptions{}}
	if got, want := sesDecode(t, flattenSESConfigurationSet(out)), sesDecode(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("flattened = %v, want SDK shape %v", got, want)
	}
}
