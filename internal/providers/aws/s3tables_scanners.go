package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/s3tables"
)

func init() {
	registerService(serviceEntry{
		name: "aws:s3tables",
		fn:   scanS3Tables,
		emits: []coverage.TypeDecl{
			{Service: "s3tables", DiscoType: TypeS3TablesTableBucket, Leaf: true},
			{Service: "s3tables", DiscoType: TypeS3TablesNamespace, Leaf: true},
			{Service: "s3tables", DiscoType: TypeS3TablesTable, Leaf: true},
			{Service: "s3tables", DiscoType: TypeS3TablesTableBucketPolicy, Leaf: true},
			{Service: "s3tables", DiscoType: TypeS3TablesTablePolicy, Leaf: true},
		},
	})
}

type s3tablesAPI interface {
	ListTableBuckets(context.Context, *s3tables.ListTableBucketsInput, ...func(*s3tables.Options)) (*s3tables.ListTableBucketsOutput, error)
	ListNamespaces(context.Context, *s3tables.ListNamespacesInput, ...func(*s3tables.Options)) (*s3tables.ListNamespacesOutput, error)
	ListTables(context.Context, *s3tables.ListTablesInput, ...func(*s3tables.Options)) (*s3tables.ListTablesOutput, error)
	GetTableBucketPolicy(context.Context, *s3tables.GetTableBucketPolicyInput, ...func(*s3tables.Options)) (*s3tables.GetTableBucketPolicyOutput, error)
	GetTablePolicy(context.Context, *s3tables.GetTablePolicyInput, ...func(*s3tables.Options)) (*s3tables.GetTablePolicyOutput, error)
}

// scanS3Tables discovers S3 Tables resources: table-bucket, namespace, table,
// and per-bucket / per-table policies.
func scanS3Tables(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3tables.NewFromConfig(acct.cfg, func(o *s3tables.Options) { o.Region = region })

	bucketARNs, t, i, ferr := scanS3TBuckets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	type tableRef struct {
		bucketARN, namespace, name string
	}
	var tables []tableRef
	for _, ba := range bucketARNs {
		t, i, ferr = scanS3TNamespaces(ctx, client, acct, region, st, scanID, ba)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		refs, tt, ii, terr := scanS3TTables(ctx, client, acct, region, st, scanID, ba)
		if terr != nil {
			return total, inserted, terr
		}
		total += tt
		inserted += ii
		for _, r := range refs {
			tables = append(tables, tableRef{ba, r.namespace, r.name})
		}

		t, i, ferr = scanS3TBucketPolicy(ctx, client, acct, region, st, scanID, ba)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	for _, tr := range tables {
		t, i, ferr = scanS3TTablePolicy(ctx, client, acct, region, st, scanID, tr.bucketARN, tr.namespace, tr.name)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanS3TBuckets(ctx context.Context, client s3tablesAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := s3tables.NewListTableBucketsPaginator(client, &s3tables.ListTableBucketsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "s3tables:ListTableBuckets", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("s3tables:ListTableBuckets: %w", err)
		}
		for _, b := range out.TableBuckets {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3TablesTableBucket, NativeID: arn,
				Name: b.Name, Region: &region,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "s3tables table-buckets")
	return arns, t, i, err
}

func scanS3TNamespaces(ctx context.Context, client s3tablesAPI, acct *account, region string, st *store.Store, scanID string, bucketARN string) (int, int, error) {
	ba := bucketARN
	pager := s3tables.NewListNamespacesPaginator(client, &s3tables.ListNamespacesInput{TableBucketARN: &ba})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "s3tables:ListNamespaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("s3tables:ListNamespaces: %w", err)
		}
		for _, n := range out.Namespaces {
			ns := strings.Join(n.Namespace, ".")
			if ns == "" {
				continue
			}
			arn := fmt.Sprintf("%s/namespace/%s", ba, ns)
			label := ns
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3TablesNamespace, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "s3tables namespaces")
}

type s3tTableID struct {
	namespace, name string
}

func scanS3TTables(ctx context.Context, client s3tablesAPI, acct *account, region string, st *store.Store, scanID string, bucketARN string) ([]s3tTableID, int, int, error) {
	ba := bucketARN
	pager := s3tables.NewListTablesPaginator(client, &s3tables.ListTablesInput{TableBucketARN: &ba})
	var batch []*store.Resource
	var ids []s3tTableID
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "s3tables:ListTables", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("s3tables:ListTables: %w", err)
		}
		for _, tt := range out.Tables {
			arn := sv(tt.TableARN)
			if arn == "" {
				continue
			}
			ns := strings.Join(tt.Namespace, ".")
			name := sv(tt.Name)
			if ns != "" && name != "" {
				ids = append(ids, s3tTableID{ns, name})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3TablesTable, NativeID: arn,
				Name: tt.Name, Region: &region,
				AttributesJSON: mustJSON(tt), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "s3tables tables")
	return ids, t, i, err
}

func scanS3TBucketPolicy(ctx context.Context, client s3tablesAPI, acct *account, region string, st *store.Store, scanID string, bucketARN string) (int, int, error) {
	out, err := client.GetTableBucketPolicy(ctx, &s3tables.GetTableBucketPolicyInput{TableBucketARN: &bucketARN})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException", "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("s3tables:GetTableBucketPolicy: %w", err)
	}
	if sv(out.ResourcePolicy) == "" {
		return 0, 0, nil
	}
	arn := bucketARN + "/policy"
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeS3TablesTableBucketPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "s3tables table-bucket-policies")
}

func scanS3TTablePolicy(ctx context.Context, client s3tablesAPI, acct *account, region string, st *store.Store, scanID string, bucketARN, namespace, name string) (int, int, error) {
	ns, nm := namespace, name
	out, err := client.GetTablePolicy(ctx, &s3tables.GetTablePolicyInput{
		TableBucketARN: &bucketARN,
		Namespace:      &ns,
		Name:           &nm,
	})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException", "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("s3tables:GetTablePolicy: %w", err)
	}
	if sv(out.ResourcePolicy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("%s/namespace/%s/table/%s/policy", bucketARN, namespace, name)
	label := name
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeS3TablesTablePolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "s3tables table-policies")
}
