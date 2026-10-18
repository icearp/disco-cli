package aws

import (
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveDSDataHierarchy,
		EdgeDecl{TypeDSMicrosoftAD, TypeDSDataUser, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSDataGroup, store.RelContains},
	)
}

// resolveDSDataHierarchy links each user and group under the directory its
// NativeID names. The scanner cannot: aws:directory-service-data runs
// alongside aws:directory-service, which may not have stored the directory
// yet, and RecordHierarchyBatch skips a pair whose parent row is missing. A
// child whose directory is not stored gets no pair. A NativeID that names no
// directory is reported after the other rows are linked.
func resolveDSDataHierarchy(acct *account, st *store.Store) error {
	dirs, err := scannedIDSet(acct, st, TypeDSMicrosoftAD)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range []struct{ typ, kind string }{{TypeDSDataUser, "user"}, {TypeDSDataGroup, "group"}} {
		bad, err := linkDSDataPages(acct, st, dirs, c.typ, c.kind)
		errs = append(errs, bad...)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
	}
	return errors.Join(errs...)
}

// dsDataResolvePageSize is how many child rows resolveDSDataHierarchy loads
// at once; a var so tests can make a page smaller than their fixture.
var dsDataResolvePageSize uint64 = 5000

// linkDSDataPages records the contains pairs of every typ row a page at a
// time, so a directory with tens of thousands of users is never loaded
// whole, as the scanner never holds it whole. Offset paging is stable here:
// the resolver writes relationships, never resources, so no row moves
// between pages. It returns the rows whose NativeID names no directory
// separately from a failed read or write, which stops the walk.
func linkDSDataPages(acct *account, st *store.Store, dirs map[string]bool, typ, kind string) ([]error, error) {
	var bad []error
	for offset := uint64(0); ; offset += dsDataResolvePageSize {
		// IncludeManaged: the well-known users and groups are provider-managed rows.
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ},
			IncludeManaged: true, Limit: dsDataResolvePageSize, Offset: offset,
		})
		if err != nil {
			return bad, err
		}
		var pairs [][2]string
		for _, r := range rows {
			dirARN, _, ok := strings.Cut(r.NativeID, "/"+kind+"/")
			if !ok {
				bad = append(bad, fmt.Errorf("%s %s: NativeID names no directory", typ, r.NativeID))
				continue
			}
			if parent := store.ResourceID("aws", acct.ID, dirARN); dirs[parent] {
				pairs = append(pairs, [2]string{r.ID, parent})
			}
		}
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return bad, fmt.Errorf("directory-service-data hierarchy: %w", err)
		}
		if uint64(len(rows)) < dsDataResolvePageSize {
			return bad, nil
		}
	}
}
