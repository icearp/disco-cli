package aws

import (
	"encoding/json"
	"errors"
	"testing"

	fsxtypes "github.com/aws/aws-sdk-go-v2/service/fsx/types"
	"github.com/icearp/disco-cli/store"
)

// fsxDecodeCase is a resolver run over a row whose attributes do not decode,
// read first, and a valid row built from the SDK element, whose one edge goes
// to target.
type fsxDecodeCase struct {
	name                 string
	resolve              func(*account, *store.Store) error
	targetType, target   string
	badType, goodType    string
	goodNative, goodAttr string
}

// runFSxDecodeCases pins, for each case, that the resolver returns the decode
// error of the undecodable row and still emits the valid row's edge.
// ListResources orders by name, so the bad row "a" is read before the valid
// row "b".
func runFSxDecodeCases(t *testing.T, cases []fsxDecodeCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			tgtID := upsertTestResource(t, st, "aws", acct.ID, c.targetType, c.target, testRegion, "{}")
			upsertTestResourceNamed(t, st, c.badType, c.goodNative+"-bad", testRegion, `["not an object"]`, "a")
			goodID := upsertTestResourceNamed(t, st, c.goodType, c.goodNative, testRegion, c.goodAttr, "b")
			var typeErr *json.UnmarshalTypeError
			if err := c.resolve(acct, st); !errors.As(err, &typeErr) {
				t.Fatalf("resolve: err = %v, want the decode error", err)
			}
			rels, err := st.RelationshipsFrom(goodID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 1 || rels[0].ToID != tgtID {
				t.Errorf("relationships of the valid row = %v; want one to %s", rels, c.target)
			}
		})
	}
}

func TestResolveFSx_DecodeErrorReturned(t *testing.T) {
	fsARN := fsxARN("aws", testRegion, testAccountID, "file-system", "fs-1")
	volARN := fsxARN("aws", testRegion, testAccountID, "volume", "fsvol-1")
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-fsx"
	runFSxDecodeCases(t, []fsxDecodeCase{
		{
			"file system refs", resolveFSxFileSystemRefs, TypeKMSKey, keyARN, TypeFSxFileSystem, TypeFSxFileSystem,
			fsxARN("aws", testRegion, testAccountID, "file-system", "fs-2"), mustJSON(fsxtypes.FileSystem{KmsKeyId: &keyARN}),
		},
		{
			// The bad row is a volume, the valid one an association: a bad row
			// of one child type costs the other types none of their edges.
			"children", resolveFSxChildrenToFileSystem, TypeFSxFileSystem, fsARN, TypeFSxVolume, TypeFSxDataRepositoryAssociation,
			fsxARN("aws", testRegion, testAccountID, "association", "dra-1"), mustJSON(fsxtypes.DataRepositoryAssociation{FileSystemId: ptrStr("fs-1")}),
		},
		{
			"snapshot", resolveFSxSnapshotToVolume, TypeFSxVolume, volARN, TypeFSxSnapshot, TypeFSxSnapshot,
			fsxARN("aws", testRegion, testAccountID, "snapshot", "fsvolsnap-1"), mustJSON(fsxtypes.Snapshot{VolumeId: ptrStr("fsvol-1")}),
		},
	})
}

func TestResolveFSxChildrenToFileSystem(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fsARN := fsxARN("aws", testRegion, acct.ID, "file-system", "fs-1")
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileSystem, fsARN, testRegion, "{}")
	vARN := fsxARN("aws", testRegion, acct.ID, "volume", "fsvol-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxVolume, vARN, testRegion, `{"FileSystemId":"fs-1"}`)
	svmARN := fsxARN("aws", testRegion, acct.ID, "storage-virtual-machine", "svm-1")
	svmID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxStorageVirtualMachine, svmARN, testRegion, `{"FileSystemId":"fs-1"}`)
	draARN := fsxARN("aws", testRegion, acct.ID, "association", "dra-1")
	draID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxDataRepositoryAssociation, draARN, testRegion, `{"FileSystemId":"fs-1"}`)

	if err := resolveFSxChildrenToFileSystem(acct, st); err != nil {
		t.Fatalf("resolveFSxChildrenToFileSystem: %v", err)
	}
	for _, c := range []string{vID, svmID, draID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, fsID, store.RelAttachedTo)
	}
}

func TestResolveFSxSnapshotToVolume(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := fsxARN("aws", testRegion, acct.ID, "volume", "fsvol-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxVolume, vARN, testRegion, "{}")
	snARN := fsxARN("aws", testRegion, acct.ID, "snapshot", "fsvolsnap-1")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxSnapshot, snARN, testRegion, `{"VolumeId":"fsvol-1"}`)
	if err := resolveFSxSnapshotToVolume(acct, st); err != nil {
		t.Fatalf("resolveFSxSnapshotToVolume: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snID)
	assertRelationship(t, rels, snID, vID, store.RelAttachedTo)
}

func TestResolveFSxFileSystemRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fsARN2 := fsxARN("aws", testRegion, acct.ID, "file-system", "fs-abc")
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-fsx"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-1")
	attrs := `{"KmsKeyId":"` + keyARN + `","VpcId":"vpc-1","SubnetIds":["subnet-1"],"NetworkInterfaceIds":["eni-1"]}`

	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileSystem, fsARN2, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, "{}")
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")

	if err := resolveFSxFileSystemRefs(acct, st); err != nil {
		t.Fatalf("resolveFSxFileSystemRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fsID)
	assertRelationship(t, rels, fsID, kID, store.RelUses)
	assertRelationship(t, rels, fsID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, fsID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, fsID, eID, store.RelAttachedTo)
}

// TestResolveFSxChildrenToFileSystem_StoreErrorKeepsDecodeErrors pins that a
// failed read of a later child type is returned together with the decode
// error already collected from an earlier type, not instead of it. The
// storage virtual machine row holds a reference_only value no bool scans
// from, so that type's ListResources fails after the volumes were read.
func TestResolveFSxChildrenToFileSystem_StoreErrorKeepsDecodeErrors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeFSxVolume, fsxARN("aws", testRegion, acct.ID, "volume", "fsvol-bad"), testRegion, `["not an object"]`)
	svmARN := fsxARN("aws", testRegion, acct.ID, "storage-virtual-machine", "svm-1")
	upsertTestResource(t, st, "aws", acct.ID, TypeFSxStorageVirtualMachine, svmARN, testRegion, mustJSON(fsxtypes.StorageVirtualMachine{FileSystemId: ptrStr("fs-1")}))
	if _, err := st.DB().Exec(`UPDATE resources SET reference_only = 'not a bool' WHERE native_id = ?`, svmARN); err != nil {
		t.Fatalf("corrupt row: %v", err)
	}
	err := resolveFSxChildrenToFileSystem(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want it to keep the volume's decode error", err)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Errorf("err = %v, want the decode error and the store error joined", err)
	}
}
