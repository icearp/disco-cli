package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"codeberg.org/icearp/disco/internal/coverage"
	"golang.org/x/sync/errgroup"
)

// serviceReferenceIndexURL is the public, credential-free AWS Service Reference
// index — the machine-readable JSON form of the IAM Service Authorization
// Reference. It lists real, SDK-discoverable resources (DynamoDB streams,
// AuditManager controls, IdentityStore users, Macie classification jobs) that
// CloudFormation's resource registry omits, so disco unions it into the AWS
// coverage upstream alongside CloudFormation ListTypes.
const serviceReferenceIndexURL = "https://servicereference.us-east-1.amazonaws.com/"

// srFetchConcurrency bounds the per-service GET fan-out. The endpoint is a
// static CDN of small JSON docs, so this is latency- not TPS-bound.
const srFetchConcurrency = 32

type srIndexEntry struct {
	Service string `json:"service"`
	URL     string `json:"url"`
}

type srServiceDoc struct {
	Name      string `json:"Name"`
	Resources []struct {
		Name string `json:"Name"`
	} `json:"Resources"`
}

// fetchServiceReference returns one coverage.UpstreamType per (service,
// resource) pair from the AWS Service Reference catalog, shaped as
// "AWS::<service>::<resource>" — the same homogeneous form disco's alias /
// AlgorithmicKey machinery produces, so coverage.Build unions and dedupes it
// against CloudFormation ListTypes case-insensitively. Credential-free: plain
// HTTPS GETs, no AWS SDK.
func fetchServiceReference(ctx context.Context) ([]coverage.UpstreamType, error) {
	return fetchServiceReferenceFrom(ctx, serviceReferenceIndexURL, http.DefaultClient)
}

// fetchServiceReferenceFrom is the httptest seam: baseURL + client injectable.
func fetchServiceReferenceFrom(ctx context.Context, baseURL string, client *http.Client) ([]coverage.UpstreamType, error) {
	index, err := srGetJSON[[]srIndexEntry](ctx, client, baseURL)
	if err != nil {
		return nil, fmt.Errorf("service reference index: %w", err)
	}

	// Per-service results are written to disjoint slots so the fan-out needs no
	// mutex; the final flatten preserves index order for stable output.
	perSvc := make([][]coverage.UpstreamType, len(index))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(srFetchConcurrency)
	for i, entry := range index {
		if entry.URL == "" {
			continue
		}
		g.Go(func() error {
			doc, derr := srGetJSON[srServiceDoc](gctx, client, entry.URL)
			if derr != nil {
				return fmt.Errorf("service reference %s: %w", entry.Service, derr)
			}
			out := make([]coverage.UpstreamType, 0, len(doc.Resources))
			for _, r := range doc.Resources {
				if r.Name == "" {
					continue
				}
				out = append(out, coverage.UpstreamType{
					Key:     "AWS::" + entry.Service + "::" + r.Name,
					Service: entry.Service,
				})
			}
			perSvc[i] = out
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return nil, werr
	}

	var out []coverage.UpstreamType
	for _, s := range perSvc {
		out = append(out, s...)
	}
	return out, nil
}

func srGetJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	var v T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return v, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return v, fmt.Errorf("decode %s: %w", url, err)
	}
	return v, nil
}
