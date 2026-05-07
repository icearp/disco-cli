package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
)

// scanLightsailExtended adds 12 disco types covering snapshots,
// networking, storage, and observability resources. Lightsail uses
// manual `pageToken` pagination across all Get* ops.
func scanLightsailExtended(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	lbNames, t, i, ferr := scanLSLoadBalancers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLSAlarms(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSBuckets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSCertificates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSDatabaseSnapshots(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSDisks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSDiskSnapshots(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSDistributions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSDomains(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLSInstanceSnapshots(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanLSLBTlsCertificates(ctx, client, acct, region, st, scanID, lbNames)
		},
		func() (int, int, error) { return scanLSStaticIps(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanLSAlarms(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetAlarms(ctx, &lightsail.GetAlarmsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetAlarms", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetAlarms: %w", err)
		}
		for _, a := range out.Alarms {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailAlarm, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail alarms")
}

func scanLSBuckets(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetBuckets(ctx, &lightsail.GetBucketsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetBuckets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetBuckets: %w", err)
		}
		for _, b := range out.Buckets {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			label := sv(b.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailBucket, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail buckets")
}

func scanLSCertificates(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetCertificates(ctx, &lightsail.GetCertificatesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "lightsail:GetCertificates", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("lightsail:GetCertificates: %w", err)
	}
	var batch []*store.Resource
	for _, c := range out.Certificates {
		arn := sv(c.CertificateArn)
		if arn == "" {
			continue
		}
		label := sv(c.CertificateName)
		if label == "" {
			label = sv(c.DomainName)
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeLightsailCertificate, NativeID: arn,
			Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "lightsail certificates")
}

func scanLSDatabaseSnapshots(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetRelationalDatabaseSnapshots(ctx, &lightsail.GetRelationalDatabaseSnapshotsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetRelationalDatabaseSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetRelationalDatabaseSnapshots: %w", err)
		}
		for _, s := range out.RelationalDatabaseSnapshots {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailDatabaseSnapshot, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail database-snapshots")
}

func scanLSDisks(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetDisks(ctx, &lightsail.GetDisksInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetDisks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetDisks: %w", err)
		}
		for _, d := range out.Disks {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailDisk, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail disks")
}

func scanLSDiskSnapshots(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetDiskSnapshots(ctx, &lightsail.GetDiskSnapshotsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetDiskSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetDiskSnapshots: %w", err)
		}
		for _, s := range out.DiskSnapshots {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailDiskSnapshot, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail disk-snapshots")
}

// scanLSDistributions: Lightsail distribution APIs are us-east-1-only —
// AWS rejects calls from any other region with `InvalidInputException:
// Distribution-related APIs are only available in the us-east-1 Region`.
// Gate the phase rather than per-region clienting since the API is
// genuinely global, just exposed solely on us-east-1.
func scanLSDistributions(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if region != "us-east-1" {
		return 0, 0, nil
	}
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetDistributions(ctx, &lightsail.GetDistributionsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetDistributions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetDistributions: %w", err)
		}
		for _, d := range out.Distributions {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailDistribution, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail distributions")
}

// scanLSDomains: Lightsail domain APIs are us-east-1-only — same gate as
// scanLSDistributions; AWS rejects calls from any other region with
// `InvalidInputException: Domain-related APIs are only available in the
// us-east-1 Region`.
func scanLSDomains(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if region != "us-east-1" {
		return 0, 0, nil
	}
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetDomains(ctx, &lightsail.GetDomainsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetDomains", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetDomains: %w", err)
		}
		for _, d := range out.Domains {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailDomain, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail domains")
}

func scanLSInstanceSnapshots(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetInstanceSnapshots(ctx, &lightsail.GetInstanceSnapshotsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetInstanceSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetInstanceSnapshots: %w", err)
		}
		for _, s := range out.InstanceSnapshots {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailInstanceSnapshot, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail instance-snapshots")
}

// scanLSLoadBalancers returns LB names so the LB-TLS-cert phase can
// fan-out (GetLoadBalancerTlsCertificates requires LoadBalancerName).
func scanLSLoadBalancers(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var names []string
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetLoadBalancers(ctx, &lightsail.GetLoadBalancersInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "lightsail:GetLoadBalancers", acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("lightsail:GetLoadBalancers: %w", err)
		}
		for _, lb := range out.LoadBalancers {
			arn := sv(lb.Arn)
			if arn == "" {
				continue
			}
			n := sv(lb.Name)
			if n != "" {
				names = append(names, n)
			}
			label := n
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailLoadBalancer, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(lb), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	t, i, err := upsertBatch(st, batch, "lightsail load-balancers")
	return names, t, i, err
}

func scanLSLBTlsCertificates(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string, lbNames []string) (int, int, error) {
	if len(lbNames) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, lbName := range lbNames {
		n := lbName
		out, err := client.GetLoadBalancerTlsCertificates(ctx, &lightsail.GetLoadBalancerTlsCertificatesInput{LoadBalancerName: &n})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("lightsail:GetLoadBalancerTlsCertificates %s: %w", lbName, err)
		}
		for _, c := range out.TlsCertificates {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.DomainName)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailLoadBalancerTLSCertificate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lightsail load-balancer-tls-certificates")
}

func scanLSStaticIps(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetStaticIps(ctx, &lightsail.GetStaticIpsInput{PageToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lightsail:GetStaticIps", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lightsail:GetStaticIps: %w", err)
		}
		for _, ip := range out.StaticIps {
			arn := sv(ip.Arn)
			if arn == "" {
				continue
			}
			label := sv(ip.Name)
			if label == "" {
				label = sv(ip.IpAddress)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLightsailStaticIP, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ip), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	return upsertBatch(st, batch, "lightsail static-ips")
}
