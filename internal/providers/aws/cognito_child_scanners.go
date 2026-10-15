package aws

import (
	"context"
	"fmt"
	"sync"

	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/icearp/disco-cli/store"
)

const (
	cognitoReplicasOp      = "cognito-idp:ListUserPoolReplicas"
	cognitoClientSecretsOp = "cognito-idp:ListUserPoolClientSecrets"
)

// isCognitoChildSkip matches the child-call errors skipped silently for one
// parent: ResourceNotFoundException (the pool or app client was deleted after
// the pool phase listed it) and the shapes a regional endpoint that does not
// route the op takes, a sub-feature gap. A bare 404 is not among them: these
// calls name a resource, so a 404 could mean a missing one.
func isCognitoChildSkip(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException", "UnknownOperationException") || isServiceNotAvailableInRegion(err)
}

// cognitoUserPoolReplicaNativeID keys a replica on the primary pool and the
// replica's region. The replica's own UserPoolArn is not used: the listing
// includes the primary itself, whose UserPoolArn is the pool row's NativeID,
// and a secondary's UserPoolArn may be the NativeID of the pool row the
// replica region's scan stores, so either would merge two rows.
func cognitoUserPoolReplicaNativeID(primaryARN, replicaRegion string) string {
	return primaryARN + "/replica/" + replicaRegion
}

// scanCognitoUserPoolReplicas lists the replicas of each pool that
// ListUserPools reported ReplicaRegions for, and stores the secondary replicas
// under the primary pool.
//
// Only the primary's listing is stored: a secondary replica's pool is listed
// by its own region's scan and lists the same replica set, which would store
// every replica a second time under another parent. A listing whose PRIMARY
// entry is in another region is therefore dropped; the primary region's scan
// stores it.
//
// OperationNotEnabledException ("not available in the current region or for
// the current user pool configuration", per the model) says this pool's
// replicas cannot be listed, not that the scan is broken, so it skips that pool
// and warns once per scan so the gap stays visible. A pool whose call
// still fails transiently after the SDK's retries is skipped, keeping its
// siblings' rows, with one summary warning.
func scanCognitoUserPoolReplicas(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, pools []childParent) (int, int, error) {
	var (
		transient  transientTally
		notEnabled sync.Once
	)
	total, inserted, err := childFanOut(ctx, st, acct, region, cognitoReplicasOp, pools, isCognitoChildSkip,
		func(ctx context.Context, pool childParent) ([]*store.Resource, error) {
			rows, err := listCognitoUserPoolReplicaRows(ctx, client, acct, region, scanID, pool)
			switch {
			case isAPIErrorCode(err, "OperationNotEnabledException"):
				notEnabled.Do(func() {
					_ = skipIfAccessDenied(st, cognitoReplicasOp, acct.ID, region, fmt.Errorf("replicas of %s not listed this scan (later pools refused the same way not reported): %w", pool.arn, err))
				})
				return nil, nil
			case isTransientNetworkError(err):
				transient.skipped(pool.arn, err)
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, cognitoReplicasOp, acct, region)
	return total, inserted, err
}

// listCognitoUserPoolReplicaRows reads every page before any row is built,
// since the PRIMARY entry that decides whether the listing is stored can be on
// any page.
func listCognitoUserPoolReplicaRows(ctx context.Context, client cognitoidpAPI, acct *account, region, scanID string, pool childParent) ([]*store.Resource, error) {
	var replicas []cognitoidptypes.UserPoolReplicaType
	in := &cognitoidp.ListUserPoolReplicasInput{UserPoolId: &pool.id}
	for {
		out, err := client.ListUserPoolReplicas(ctx, in)
		if err != nil {
			return nil, err
		}
		replicas = append(replicas, out.UserPoolReplicas...)
		// ListUserPoolReplicas has no paginator; a repeated token would loop
		// forever.
		if next := sv(out.NextToken); next == "" || next == sv(in.NextToken) {
			return cognitoReplicaRows(acct, region, scanID, pool, replicas), nil
		}
		in.NextToken = out.NextToken
	}
}

// cognitoReplicaRows builds a row for every non-PRIMARY replica, or none when
// the PRIMARY entry is not in region (see scanCognitoUserPoolReplicas).
func cognitoReplicaRows(acct *account, region, scanID string, pool childParent, replicas []cognitoidptypes.UserPoolReplicaType) []*store.Resource {
	primaryHere := false
	for _, r := range replicas {
		if r.Role == cognitoidptypes.ReplicaRoleTypePrimary && sv(r.RegionName) == region {
			primaryHere = true
			break
		}
	}
	if !primaryHere {
		return nil
	}
	var rows []*store.Resource
	for _, r := range replicas {
		replicaRegion := sv(r.RegionName)
		if r.Role == cognitoidptypes.ReplicaRoleTypePrimary || replicaRegion == "" {
			continue
		}
		// The row lives in the primary's region, the only scan that stores it,
		// and is named by the replica's region; a failover that moves the
		// PRIMARY role re-keys every replica row under the new primary.
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type:     TypeCognitoUserPoolReplica,
			NativeID: cognitoUserPoolReplicaNativeID(pool.arn, replicaRegion),
			Name:     &replicaRegion, Region: &region, Status: nonEmptyPtr(string(r.Status)),
			AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
		})
	}
	return rows
}

// scanCognitoUserPoolClientSecrets lists the client secrets of each
// confidential app client the pool phase stored. The op returns only each
// secret's id and creation date, never its value (the model's documentation
// says so, and the value field is @sensitive); the value field is still
// redacted by the type's descriptor in case a response ever carries it.
//
// A secret has no ARN, so its NativeID is {clientARN}/secret/{ClientSecretId}.
// A client whose call still fails transiently after the SDK's retries is
// skipped, keeping its siblings' rows, with one summary warning.
func scanCognitoUserPoolClientSecrets(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, clients []cognitoAppClientRef) (int, int, error) {
	parents := make([]childParent, len(clients))
	poolOf := make(map[string]string, len(clients))
	for i, c := range clients {
		parents[i] = childParent{id: c.clientID, arn: c.arn}
		poolOf[c.arn] = c.poolID
	}
	var transient transientTally
	total, inserted, err := childFanOut(ctx, st, acct, region, cognitoClientSecretsOp, parents, isCognitoChildSkip,
		func(ctx context.Context, appClient childParent) ([]*store.Resource, error) {
			rows, err := listCognitoClientSecretRows(ctx, client, acct, region, scanID, poolOf[appClient.arn], appClient)
			if isTransientNetworkError(err) {
				transient.skipped(appClient.arn, err)
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, cognitoClientSecretsOp, acct, region)
	return total, inserted, err
}

func listCognitoClientSecretRows(ctx context.Context, client cognitoidpAPI, acct *account, region, scanID, poolID string, appClient childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	in := &cognitoidp.ListUserPoolClientSecretsInput{UserPoolId: &poolID, ClientId: &appClient.id}
	for {
		out, err := client.ListUserPoolClientSecrets(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, s := range out.ClientSecrets {
			id := sv(s.ClientSecretId)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeCognitoUserPoolClientSecret,
				NativeID: appClient.arn + "/secret/" + id,
				Name:     &id, Region: &region, CreatedAt: tp(s.ClientSecretCreateDate),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		// ListUserPoolClientSecrets has no paginator; a repeated token would
		// loop forever.
		if next := sv(out.NextToken); next == "" || next == sv(in.NextToken) {
			return rows, nil
		}
		in.NextToken = out.NextToken
	}
}
