package aws

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/fsx"
	fsxtypes "github.com/aws/aws-sdk-go-v2/service/fsx/types"
	"github.com/icearp/disco-cli/store"
)

var fsxTestTags = map[string]string{"env": "prod", "owner": ""}

// fsxTestElem is one listed FSx element: an empty field stays unset on the
// SDK element.
type fsxTestElem struct {
	arn, id, name, lifecycle string
	tags                     []fsxtypes.Tag
}

// fsxCase is one listing scanFSxAll stores rows from. elem builds the SDK
// element; out wraps elements in the op's output.
type fsxCase struct {
	op, typ    string
	maxResults int32
	nameIsID   bool // the row's Name is the element's id; otherwise its Name
	elem       func(e fsxTestElem) any
	out        func(elems []any) any
	noTags     bool                       // the element carries no tags
	nativeID   func(e fsxTestElem) string // nil: the element's ResourceARN
}

// fsxLabel is the op label c's warnings and errors are reported under,
// spelled out here so a changed label constant fails the tests.
func fsxLabel(c fsxCase) string { return "fsx:" + c.op }

// key is the NativeID of e's row.
func (c fsxCase) key(e fsxTestElem) string {
	if c.nativeID != nil {
		return c.nativeID(e)
	}
	return e.arn
}

var fsxCases = []fsxCase{
	{
		"DescribeFileSystems", TypeFSxFileSystem, math.MaxInt32, true,
		func(e fsxTestElem) any {
			return fsxtypes.FileSystem{ResourceARN: nilIfEmpty(e.arn), FileSystemId: nilIfEmpty(e.id), Lifecycle: fsxtypes.FileSystemLifecycle(e.lifecycle), Tags: e.tags}
		},
		func(elems []any) any {
			o := &fsx.DescribeFileSystemsOutput{}
			for _, e := range elems {
				o.FileSystems = append(o.FileSystems, e.(fsxtypes.FileSystem))
			}
			return o
		},
		false, nil,
	},
	{
		"DescribeDataRepositoryAssociations", TypeFSxDataRepositoryAssociation, 25, true,
		func(e fsxTestElem) any {
			return fsxtypes.DataRepositoryAssociation{ResourceARN: nilIfEmpty(e.arn), AssociationId: nilIfEmpty(e.id), Lifecycle: fsxtypes.DataRepositoryLifecycle(e.lifecycle), Tags: e.tags}
		},
		func(elems []any) any {
			o := &fsx.DescribeDataRepositoryAssociationsOutput{}
			for _, e := range elems {
				o.Associations = append(o.Associations, e.(fsxtypes.DataRepositoryAssociation))
			}
			return o
		},
		false, nil,
	},
	{
		"DescribeSnapshots", TypeFSxSnapshot, math.MaxInt32, false,
		func(e fsxTestElem) any {
			return fsxtypes.Snapshot{ResourceARN: nilIfEmpty(e.arn), SnapshotId: nilIfEmpty(e.id), Name: nilIfEmpty(e.name), Lifecycle: fsxtypes.SnapshotLifecycle(e.lifecycle), Tags: e.tags}
		},
		func(elems []any) any {
			o := &fsx.DescribeSnapshotsOutput{}
			for _, e := range elems {
				o.Snapshots = append(o.Snapshots, e.(fsxtypes.Snapshot))
			}
			return o
		},
		false, nil,
	},
	{
		"DescribeStorageVirtualMachines", TypeFSxStorageVirtualMachine, math.MaxInt32, false,
		func(e fsxTestElem) any {
			return fsxtypes.StorageVirtualMachine{ResourceARN: nilIfEmpty(e.arn), StorageVirtualMachineId: nilIfEmpty(e.id), Name: nilIfEmpty(e.name), Lifecycle: fsxtypes.StorageVirtualMachineLifecycle(e.lifecycle), Tags: e.tags}
		},
		func(elems []any) any {
			o := &fsx.DescribeStorageVirtualMachinesOutput{}
			for _, e := range elems {
				o.StorageVirtualMachines = append(o.StorageVirtualMachines, e.(fsxtypes.StorageVirtualMachine))
			}
			return o
		},
		false, nil,
	},
	{
		"DescribeVolumes", TypeFSxVolume, math.MaxInt32, false,
		func(e fsxTestElem) any {
			return fsxtypes.Volume{ResourceARN: nilIfEmpty(e.arn), VolumeId: nilIfEmpty(e.id), Name: nilIfEmpty(e.name), Lifecycle: fsxtypes.VolumeLifecycle(e.lifecycle), Tags: e.tags}
		},
		func(elems []any) any {
			o := &fsx.DescribeVolumesOutput{}
			for _, e := range elems {
				o.Volumes = append(o.Volumes, e.(fsxtypes.Volume))
			}
			return o
		},
		false, nil,
	},
	{
		"DescribeBackups", TypeFSxBackup, math.MaxInt32, true,
		func(e fsxTestElem) any {
			return fsxtypes.Backup{ResourceARN: nilIfEmpty(e.arn), BackupId: nilIfEmpty(e.id), Lifecycle: fsxtypes.BackupLifecycle(e.lifecycle), Tags: e.tags}
		},
		func(elems []any) any {
			o := &fsx.DescribeBackupsOutput{}
			for _, e := range elems {
				o.Backups = append(o.Backups, e.(fsxtypes.Backup))
			}
			return o
		},
		false, nil,
	},
	{
		op: "DescribeS3AccessPointAttachments", typ: TypeFSxS3AccessPointAttachment,
		maxResults: math.MaxInt32, noTags: true,
		elem: func(e fsxTestElem) any {
			return fsxtypes.S3AccessPointAttachment{Name: nilIfEmpty(e.name), Lifecycle: fsxtypes.S3AccessPointAttachmentLifecycle(e.lifecycle)}
		},
		out: func(elems []any) any {
			o := &fsx.DescribeS3AccessPointAttachmentsOutput{}
			for _, e := range elems {
				o.S3AccessPointAttachments = append(o.S3AccessPointAttachments, e.(fsxtypes.S3AccessPointAttachment))
			}
			return o
		},
		nativeID: func(e fsxTestElem) string {
			return "arn:aws:fsx:us-east-1:123456789012:s3-access-point-attachment/" + e.name
		},
	},
	{
		op: "DescribeFileCaches", typ: TypeFSxFileCache,
		maxResults: math.MaxInt32, nameIsID: true, noTags: true,
		elem: func(e fsxTestElem) any {
			return fsxtypes.FileCache{ResourceARN: nilIfEmpty(e.arn), FileCacheId: nilIfEmpty(e.id), Lifecycle: fsxtypes.FileCacheLifecycle(e.lifecycle)}
		},
		out: func(elems []any) any {
			o := &fsx.DescribeFileCachesOutput{}
			for _, e := range elems {
				o.FileCaches = append(o.FileCaches, e.(fsxtypes.FileCache))
			}
			return o
		},
	},
}

// fsxTagged is the first page's element of c: named, in a lifecycle state and
// tagged. fsxUntagged is the second page's: none of the three.
func fsxTagged(c fsxCase) fsxTestElem {
	return fsxTestElem{
		arn: "arn:aws:fsx:us-east-1:123456789012:" + c.op + "/one", id: c.op + "-1", name: c.op + " one", lifecycle: "AVAILABLE",
		tags: []fsxtypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("owner")}},
	}
}

func fsxUntagged(c fsxCase) fsxTestElem {
	e := fsxTestElem{arn: "arn:aws:fsx:us-east-1:123456789012:" + c.op + "/two", id: c.op + "-2"}
	if c.nativeID != nil {
		e.name = c.op + " two"
	}
	return e
}

// fsxServe serves c's listing as two pages: a call without NextToken gets
// page1 with NextToken "t1", a call with "t1" gets page2 as the last page. A
// token not forwarded therefore repeats page one and ends the listing with
// errRepeatedNextToken. Every call must ask for the model's MaxResults cap and
// leave every other input unset (the ids and filters narrow the listing;
// IncludeShared adds snapshots shared from other accounts).
func fsxServe(t *testing.T, c fsxCase, page1, page2 []fsxTestElem) func(in any) (any, error) {
	build := func(elems []fsxTestElem) any {
		items := make([]any, 0, len(elems))
		for _, e := range elems {
			items = append(items, c.elem(e))
		}
		return c.out(items)
	}
	return func(in any) (any, error) {
		req := reflect.ValueOf(in).Elem()
		for i := range req.NumField() {
			name := req.Type().Field(i).Name
			f := req.Field(i)
			switch {
			case name == "NextToken":
			case name == "MaxResults":
				if mr := f.Interface().(*int32); mr == nil || *mr != c.maxResults {
					t.Errorf("%s MaxResults = %v, want %d", c.op, mr, c.maxResults)
				}
			case !f.IsZero():
				t.Errorf("%s %s = %v, want unset", c.op, name, f.Interface())
			}
		}
		switch token := sv(req.FieldByName("NextToken").Interface().(*string)); token {
		case "":
			out := build(page1)
			reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(ptrStr("t1")))
			return out, nil
		case "t1":
			return build(page2), nil
		default:
			t.Errorf("%s NextToken = %q, want t1", c.op, token)
			return nil, errors.New("bad token")
		}
	}
}

// fsxHandlers serves every listing scanFSxAll calls as two pages: fsxTagged,
// then fsxUntagged.
func fsxHandlers(t *testing.T) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){}
	for _, c := range fsxCases {
		h[c.op] = fsxServe(t, c, []fsxTestElem{fsxTagged(c)}, []fsxTestElem{fsxUntagged(c)})
	}
	return h
}

func fsxScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	client := fsx.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	return scanFSxAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
}

// assertJSONEqual fails unless got and want encode the same JSON value.
func assertJSONEqual(t *testing.T, what, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("%s: invalid JSON %q: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: invalid want JSON %q: %v", what, want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// TestScanFSxAll_StoresRows pins, for every listing, the stored row of a
// tagged element (NativeID, Name, Status, tags, the element verbatim as
// attributes) and of an untagged one with no lifecycle (no tags, no status),
// read from two pages.
func TestScanFSxAll_StoresRows(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := fsxScanAll(t, st, fsxHandlers(t))
	if err != nil {
		t.Fatalf("scanFSxAll: %v", err)
	}
	if total != 16 || inserted != 16 {
		t.Errorf("total, inserted = %d, %d; want 16, 16", total, inserted)
	}
	for _, c := range fsxCases {
		t.Run(c.op, func(t *testing.T) {
			rows := rowsByNativeID(t, st, c.typ)
			if len(rows) != 2 {
				t.Errorf("stored %d rows, want 2", len(rows))
			}
			tagged, untagged := fsxTagged(c), fsxUntagged(c)
			r, ok := rows[c.key(tagged)]
			if !ok {
				t.Fatalf("%s not stored", c.key(tagged))
			}
			wantName := tagged.name
			if c.nameIsID {
				wantName = tagged.id
			}
			if got := sv(r.Name); got != wantName {
				t.Errorf("Name = %q, want %q", got, wantName)
			}
			if got := sv(r.Status); got != "AVAILABLE" {
				t.Errorf("Status = %q, want AVAILABLE", got)
			}
			if c.noTags {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %s, want nil", *r.TagsJSON)
				}
			} else {
				assertTagsMap(t, r.TagsJSON, fsxTestTags)
			}
			assertJSONEqual(t, "AttributesJSON", r.AttributesJSON, mustJSON(c.elem(tagged)))

			u, ok := rows[c.key(untagged)]
			if !ok {
				t.Fatalf("%s not stored", c.key(untagged))
			}
			if u.TagsJSON != nil {
				t.Errorf("untagged TagsJSON = %s, want nil", *u.TagsJSON)
			}
			if u.Status != nil {
				t.Errorf("untagged Status = %q, want nil", *u.Status)
			}
			switch {
			case c.nameIsID:
				if got := sv(u.Name); got != untagged.id {
					t.Errorf("untagged Name = %q, want %q", got, untagged.id)
				}
			case untagged.name != "":
				if got := sv(u.Name); got != untagged.name {
					t.Errorf("untagged Name = %q, want %q", got, untagged.name)
				}
			case u.Name != nil:
				t.Errorf("untagged Name = %q, want nil", *u.Name)
			}
			assertJSONEqual(t, "untagged AttributesJSON", u.AttributesJSON, mustJSON(c.elem(untagged)))
		})
	}
}

// TestScanFSxAll_LaterPageFailureKeepsRows pins, for every listing, that a
// failure on the second page is returned and the first page's row is kept.
func TestScanFSxAll_LaterPageFailureKeepsRows(t *testing.T) {
	hard := apiErr("BadRequest", "bad")
	for _, c := range fsxCases {
		t.Run(c.op, func(t *testing.T) {
			h := fsxHandlers(t)
			serve := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if sv(reflect.ValueOf(in).Elem().FieldByName("NextToken").Interface().(*string)) != "" {
					return nil, hard
				}
				return serve(in)
			}
			st := newTestStore(t)
			total, _, err := fsxScanAll(t, st, h)
			if !errors.Is(err, hard) {
				t.Fatalf("err = %v, want the BadRequest", err)
			}
			if total != 15 {
				t.Errorf("total = %d, want 15", total)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.key(fsxTagged(c))]; !ok || len(rows) != 1 {
				t.Errorf("stored %v, want only the first page's %s", rows, c.key(fsxTagged(c)))
			}
		})
	}
}

// TestScanFSxAll_DeniedWarns pins, for every listing, that a denial is one
// warning under the op's label with the account/region scope, not an error,
// and that the other listings are stored.
func TestScanFSxAll_DeniedWarns(t *testing.T) {
	for _, c := range fsxCases {
		t.Run(c.op, func(t *testing.T) {
			h := fsxHandlers(t)
			h[c.op] = func(any) (any, error) {
				return nil, apiErr("AccessDenied", "User: x is not authorized to perform: "+fsxLabel(c))
			}
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == fsxLabel(c) {
					scopes = append(scopes, w.Scope)
				}
			}
			total, _, err := fsxScanAll(t, st, h)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(scopes) != 1 || scopes[0] != testAccountID+"/"+testRegion {
				t.Errorf("warning scopes = %v, want one %s/%s", scopes, testAccountID, testRegion)
			}
			if total != 14 {
				t.Errorf("total = %d, want the other listings' 14", total)
			}
		})
	}
}

// TestScanFSxAll_EmptyARNWarned pins, for every listing, that an element
// listed without the field its NativeID comes from (ResourceARN; Name for S3
// access point attachments) is not stored and is warned about once under the
// op's label, while its siblings are stored.
func TestScanFSxAll_EmptyARNWarned(t *testing.T) {
	for _, c := range fsxCases {
		t.Run(c.op, func(t *testing.T) {
			h := fsxHandlers(t)
			noARN := fsxTestElem{id: c.op + "-noarn"}
			h[c.op] = fsxServe(t, c, []fsxTestElem{fsxTagged(c), noARN}, []fsxTestElem{fsxUntagged(c)})
			st := newTestStore(t)
			var msgs []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == fsxLabel(c) {
					msgs = append(msgs, w.Message)
				}
			}
			if _, _, err := fsxScanAll(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(msgs) != 1 {
				t.Errorf("warnings = %v, want one", msgs)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 2 {
				t.Errorf("stored %d rows, want the 2 with an ARN", n)
			}
		})
	}
}

// TestScanFSxAll_StoreWriteStops pins, for every listing, that a failed write
// is returned as ErrStoreWrite with no empty-ARN warning (it would describe
// rows the write lost) and that no later listing calls AWS.
func TestScanFSxAll_StoreWriteStops(t *testing.T) {
	order := []string{
		"DescribeFileSystems", "DescribeDataRepositoryAssociations", "DescribeSnapshots",
		"DescribeStorageVirtualMachines", "DescribeVolumes", "DescribeS3AccessPointAttachments", "DescribeBackups", "DescribeFileCaches",
	}
	for _, c := range fsxCases {
		t.Run(c.op, func(t *testing.T) {
			var calls sync.Map
			h := fsxHandlers(t)
			h[c.op] = fsxServe(t, c, []fsxTestElem{fsxTagged(c), {id: c.op + "-noarn"}}, []fsxTestElem{fsxUntagged(c)})
			for op, f := range h {
				h[op] = func(in any) (any, error) {
					calls.Store(op, true)
					return f(in)
				}
			}
			st, closeStore := openClosingStore(t)
			serve := h[c.op]
			h[c.op] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			warns := countOpWarnings(st, fsxLabel(c))
			if _, _, err := fsxScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d after the failed write, want 0", *warns)
			}
			after := false
			for _, op := range order {
				if after {
					if _, called := calls.Load(op); called {
						t.Errorf("%s called after the failed write, want not", op)
					}
				}
				after = after || op == c.op
			}
		})
	}
}

// TestScanFSxAll_PhaseErrorRunsLaterPhases pins, for every listing, that a
// failed first page is returned and every other listing is still stored.
func TestScanFSxAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	hard := apiErr("BadRequest", "bad")
	for _, c := range fsxCases {
		t.Run(c.op, func(t *testing.T) {
			h := fsxHandlers(t)
			h[c.op] = func(any) (any, error) { return nil, hard }
			st := newTestStore(t)
			if _, _, err := fsxScanAll(t, st, h); !errors.Is(err, hard) {
				t.Fatalf("err = %v, want the BadRequest", err)
			}
			for _, o := range fsxCases {
				want := 2
				if o.op == c.op {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, o.typ)); n != want {
					t.Errorf("stored %d %s, want %d", n, o.typ, want)
				}
			}
		})
	}
}

// TestScanFSxAll_TransientBesideHardError pins that a throttled listing beside
// a hard-failing one is demoted to one warning under its label and the scan
// classified as an error, not as transient.
func TestScanFSxAll_TransientBesideHardError(t *testing.T) {
	h := fsxHandlers(t)
	h["DescribeFileSystems"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["DescribeBackups"] = func(any) (any, error) { return nil, apiErr("BadRequest", "bad") }
	st := newTestStore(t)
	warns := countOpWarnings(st, "fsx:DescribeFileSystems")
	_, _, err := fsxScanAll(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d, want 1", *warns)
	}
}

// TestScanFSxAll_EmptyIDStoredWithoutName pins, for every listing whose row
// is named after the element's id, that an element listed with an ARN but no
// id is stored with no Name rather than an empty one.
func TestScanFSxAll_EmptyIDStoredWithoutName(t *testing.T) {
	for _, c := range fsxCases {
		if !c.nameIsID {
			continue
		}
		t.Run(c.op, func(t *testing.T) {
			h := fsxHandlers(t)
			noID := fsxTestElem{arn: "arn:aws:fsx:us-east-1:123456789012:" + c.op + "/noid"}
			h[c.op] = fsxServe(t, c, []fsxTestElem{fsxTagged(c), noID}, []fsxTestElem{fsxUntagged(c)})
			st := newTestStore(t)
			if _, _, err := fsxScanAll(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[noID.arn]
			if !ok {
				t.Fatalf("%s not stored", noID.arn)
			}
			if r.Name != nil {
				t.Errorf("Name = %q, want nil", *r.Name)
			}
		})
	}
}

// fsxFileCacheCase is the file cache listing among fsxCases.
func fsxFileCacheCase(t *testing.T) fsxCase {
	t.Helper()
	for _, c := range fsxCases {
		if c.op == "DescribeFileCaches" {
			return c
		}
	}
	t.Fatal("no DescribeFileCaches case")
	return fsxCase{}
}

// TestScanFSxAll_FileCacheNotEnabledSkipped pins that BadRequest "The
// requested feature is not enabled for this AWS account" on the first file
// cache page is an empty listing, with no error and no warning, while on a
// later page it is returned with the first page's row kept.
func TestScanFSxAll_FileCacheNotEnabledSkipped(t *testing.T) {
	notEnabled := apiErr("BadRequest", "The requested feature is not enabled for this AWS account")
	c := fsxFileCacheCase(t)
	t.Run("first page", func(t *testing.T) {
		h := fsxHandlers(t)
		h[c.op] = func(any) (any, error) { return nil, notEnabled }
		st := newTestStore(t)
		warns := countOpWarnings(st, fsxLabel(c))
		total, _, err := fsxScanAll(t, st, h)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if total != 14 || *warns != 0 {
			t.Errorf("total, warnings = %d, %d; want 14, 0", total, *warns)
		}
	})
	t.Run("later page", func(t *testing.T) {
		h := fsxHandlers(t)
		serve := h[c.op]
		h[c.op] = func(in any) (any, error) {
			if in.(*fsx.DescribeFileCachesInput).NextToken != nil {
				return nil, notEnabled
			}
			return serve(in)
		}
		st := newTestStore(t)
		if _, _, err := fsxScanAll(t, st, h); !errors.Is(err, notEnabled) {
			t.Fatalf("err = %v, want the BadRequest", err)
		}
		if n := len(rowsByNativeID(t, st, c.typ)); n != 1 {
			t.Errorf("stored %d file caches, want the first page's 1", n)
		}
	})
}
