package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

var kmsDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: kms:List")

const (
	kmsHSMStoreID = "cks-1111111111111111a"
	kmsXKSStoreID = "cks-2222222222222222b"
	kmsTestKeyID  = "1234abcd-12ab-34cd-56ef-1234567890ab"
)

func newKMSTestClient(handlers map[string]func(in any) (any, error)) *kms.Client {
	return kms.NewFromConfig(opStubConfig(handlers, kmsDenied))
}

// kmsKeyStorePages answers DescribeCustomKeyStores with pages chained by
// Marker "1", "2", ..., recording each request's Limit.
func kmsKeyStorePages(limits *[]int32, mu *sync.Mutex, pages ...[]kmstypes.CustomKeyStoresListEntry) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*kms.DescribeCustomKeyStoresInput)
		mu.Lock()
		*limits = append(*limits, sdkaws.ToInt32(req.Limit))
		mu.Unlock()
		idx := 0
		if m := sdkaws.ToString(req.Marker); m != "" {
			n, err := strconv.Atoi(m)
			if err != nil {
				return nil, fmt.Errorf("unexpected marker %q", m)
			}
			idx = n
		}
		out := &kms.DescribeCustomKeyStoresOutput{}
		if idx < len(pages) {
			out.CustomKeyStores = pages[idx]
		}
		if idx+1 < len(pages) {
			out.NextMarker = sdkaws.String(strconv.Itoa(idx + 1))
			out.Truncated = true
		}
		return out, nil
	}
}

func kmsHSMStore() kmstypes.CustomKeyStoresListEntry {
	return kmstypes.CustomKeyStoresListEntry{
		CustomKeyStoreId:       sdkaws.String(kmsHSMStoreID),
		CustomKeyStoreName:     sdkaws.String("hsm-store"),
		CustomKeyStoreType:     kmstypes.CustomKeyStoreTypeAwsCloudhsm,
		CloudHsmClusterId:      sdkaws.String("cluster-234567abcdefg"),
		TrustAnchorCertificate: sdkaws.String("-----BEGIN CERTIFICATE-----MIIB-----END CERTIFICATE-----"),
		ConnectionState:        kmstypes.ConnectionStateTypeConnected,
		CreationDate:           sdkaws.Time(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)),
	}
}

func kmsXKSStore() kmstypes.CustomKeyStoresListEntry {
	return kmstypes.CustomKeyStoresListEntry{
		CustomKeyStoreId:   sdkaws.String(kmsXKSStoreID),
		CustomKeyStoreName: sdkaws.String("xks-store"),
		CustomKeyStoreType: kmstypes.CustomKeyStoreTypeExternalKeyStore,
		XksProxyConfiguration: &kmstypes.XksProxyConfigurationType{
			AccessKeyId:             sdkaws.String("ABCDE12345FGHIJ67890"),
			Connectivity:            kmstypes.XksProxyConnectivityTypeVpcEndpointService,
			UriEndpoint:             sdkaws.String("https://xks.example.com"),
			UriPath:                 sdkaws.String("/kms/xks/v1"),
			VpcEndpointServiceName:  sdkaws.String("com.amazonaws.vpce.us-east-1.vpce-svc-0123456789abcdef0"),
			VpcEndpointServiceOwner: sdkaws.String(testAccountID),
		},
	}
}

func TestScanKMSCustomKeyStores_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var (
		mu     sync.Mutex
		limits []int32
	)
	client := newKMSTestClient(map[string]func(any) (any, error){
		"DescribeCustomKeyStores": kmsKeyStorePages(&limits, &mu,
			[]kmstypes.CustomKeyStoresListEntry{kmsHSMStore(), {CustomKeyStoreName: sdkaws.String("no-id")}},
			[]kmstypes.CustomKeyStoresListEntry{kmsXKSStore()},
		),
	})

	total, _, err := scanKMSCustomKeyStores(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanKMSCustomKeyStores: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (the id-less element skipped)", total)
	}
	if len(limits) != 2 || limits[0] != 1000 || limits[1] != 1000 {
		t.Errorf("request limits = %v; want [1000 1000] (the model cap, both pages)", limits)
	}
	rows := rowsByNativeID(t, st, TypeKMSCustomKeyStore)
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2: %v", len(rows), rows)
	}
	hsmID := "arn:aws:kms:us-east-1:123456789012:custom-key-store/" + kmsHSMStoreID
	xksID := "arn:aws:kms:us-east-1:123456789012:custom-key-store/" + kmsXKSStoreID
	hsm, ok := rows[hsmID]
	if !ok {
		t.Fatalf("missing %s in %v", hsmID, rows)
	}
	if hsm.Type != TypeKMSCustomKeyStore || sv(hsm.Region) != testRegion || sv(hsm.Name) != "hsm-store" || sv(hsm.Status) != "CONNECTED" {
		t.Errorf("hsm row Type %q Region %q Name %q Status %q", hsm.Type, sv(hsm.Region), sv(hsm.Name), sv(hsm.Status))
	}
	if got := sv(hsm.CreatedAt); got != "2026-03-04T05:06:07Z" {
		t.Errorf("hsm CreatedAt = %q; want 2026-03-04T05:06:07Z", got)
	}
	if !strings.Contains(hsm.AttributesJSON, `"CloudHsmClusterId":"cluster-234567abcdefg"`) {
		t.Errorf("hsm attrs = %s; want CloudHsmClusterId", hsm.AttributesJSON)
	}
	xks, ok := rows[xksID]
	if !ok {
		t.Fatalf("missing %s in %v", xksID, rows)
	}
	if xks.Status != nil {
		t.Errorf("xks Status = %q; want unset for an empty ConnectionState", sv(xks.Status))
	}
	// The access key id is the non-secret half of the proxy credential and is
	// kept through the store's redaction pass.
	if !strings.Contains(xks.AttributesJSON, `"AccessKeyId":"ABCDE12345FGHIJ67890"`) {
		t.Errorf("xks attrs = %s; want the AccessKeyId kept", xks.AttributesJSON)
	}
}

func TestScanKMSCustomKeyStores_Empty(t *testing.T) {
	st := newTestStore(t)
	var (
		mu     sync.Mutex
		limits []int32
	)
	client := newKMSTestClient(map[string]func(any) (any, error){
		"DescribeCustomKeyStores": kmsKeyStorePages(&limits, &mu),
	})
	total, _, err := scanKMSCustomKeyStores(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeKMSCustomKeyStore)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

func TestScanKMSCustomKeyStores_DeniedWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "kms:DescribeCustomKeyStores")
	client := newKMSTestClient(nil)
	total, _, err := scanKMSCustomKeyStores(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1", *warns)
	}
}

func TestScanKMSCustomKeyStores_OtherErrorReturned(t *testing.T) {
	st := newTestStore(t)
	client := newKMSTestClient(map[string]func(any) (any, error){
		"DescribeCustomKeyStores": func(any) (any, error) { return nil, apiErr("KMSInternalException", "boom") },
	})
	_, _, err := scanKMSCustomKeyStores(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "KMSInternalException") {
		t.Errorf("err = %v; want KMSInternalException", err)
	}
}

// kmsTopLevelHandlers answers the key phases with one customer key and no
// aliases, and the key store phase with one CloudHSM store. Ops left out
// (GetKeyPolicy, GetKeyRotationStatus, ListGrants) are denied, which the key
// phase tolerates.
func kmsTopLevelHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListKeys": func(any) (any, error) {
			return &kms.ListKeysOutput{Keys: []kmstypes.KeyListEntry{{KeyId: sdkaws.String(kmsTestKeyID)}}}, nil
		},
		"DescribeKey": func(any) (any, error) {
			return &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{
				KeyId:      sdkaws.String(kmsTestKeyID),
				Arn:        sdkaws.String("arn:aws:kms:us-east-1:123456789012:key/" + kmsTestKeyID),
				KeyManager: kmstypes.KeyManagerTypeCustomer,
			}}, nil
		},
		"ListAliases": func(any) (any, error) { return &kms.ListAliasesOutput{}, nil },
		"DescribeCustomKeyStores": func(any) (any, error) {
			return &kms.DescribeCustomKeyStoresOutput{CustomKeyStores: []kmstypes.CustomKeyStoresListEntry{kmsHSMStore()}}, nil
		},
	}
}

func TestScanKMS_StoresKeysAndCustomKeyStores(t *testing.T) {
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(kmsTopLevelHandlers(), kmsDenied)}

	total, _, err := scanKMS(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanKMS: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (1 key + 1 key store)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeKMSKey)); n != 1 {
		t.Errorf("key rows = %d; want 1", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeKMSCustomKeyStore)[kmsCustomKeyStoreNativeID(testRegion, testAccountID, kmsHSMStoreID)]; !ok {
		t.Error("custom key store row missing")
	}
}

// Key stores are listed before the key fan-out, so a deadline that expires
// during the keys does not lose them.
func TestScanKMS_KeyStoresListedBeforeKeyDeadline(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := kmsTopLevelHandlers()
	h["ListKeys"] = func(any) (any, error) {
		cancel()
		return nil, context.DeadlineExceeded
	}
	withCtx := ignoreCtx(h)
	listStores := withCtx["DescribeCustomKeyStores"]
	withCtx["DescribeCustomKeyStores"] = func(ctx context.Context, in any) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return listStores(ctx, in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(withCtx, kmsDenied)}

	_, _, err := scanKMS(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v; want the key phase's deadline", err)
	}
	if n := len(rowsByNativeID(t, st, TypeKMSCustomKeyStore)); n != 1 {
		t.Errorf("key store rows = %d; want 1", n)
	}
}

// A hard key-phase error still leaves the key store phase to run, and both
// phases' errors are returned.
func TestScanKMS_KeyPhaseErrorStillScansKeyStores(t *testing.T) {
	st := newTestStore(t)
	h := kmsTopLevelHandlers()
	h["ListKeys"] = func(any) (any, error) { return nil, apiErr("KMSInternalException", "keys down") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, kmsDenied)}

	total, _, err := scanKMS(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "KMSInternalException") {
		t.Errorf("err = %v; want the ListKeys KMSInternalException", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 (the key store)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeKMSCustomKeyStore)); n != 1 {
		t.Errorf("key store rows = %d; want 1", n)
	}
}

// A transient key-phase error is demoted to a warning, so the key store
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanKMS_TransientKeyPhaseDoesNotMaskKeyStoreError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "kms")
	h := kmsTopLevelHandlers()
	h["ListKeys"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["DescribeCustomKeyStores"] = func(any) (any, error) { return nil, apiErr("KMSInternalException", "boom") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, kmsDenied)}

	_, _, err := scanKMS(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "KMSInternalException") {
		t.Errorf("err = %v; want the key store phase's KMSInternalException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
}

// A transient key store error is demoted to a warning under its op instead of
// buried in the key phase's hard error, and the key rows stay.
func TestScanKMS_TransientKeyStorePhaseDoesNotMaskKeyError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "kms:DescribeCustomKeyStores")
	h := kmsTopLevelHandlers()
	h["ListAliases"] = func(any) (any, error) { return nil, apiErr("KMSInternalException", "aliases down") }
	h["DescribeCustomKeyStores"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, kmsDenied)}

	total, _, err := scanKMS(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 (the key)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeKMSKey)); n != 1 {
		t.Errorf("key rows = %d; want 1", n)
	}
}
