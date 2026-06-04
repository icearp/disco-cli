//go:build paid

// RDS IAM database-authentication support for the Postgres backend. When
// DISCO_PG_IAM_AUTH is set, the scanner authenticates to Postgres with a
// short-lived IAM token instead of a password, so no DB password needs to be
// provisioned for the scanner task. Tokens (~15 min TTL) gate only the
// handshake, so a fresh one is minted before every physical connection via
// the pgx BeforeConnect hook.
package store

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5"
)

// iamAuthEnabled reports whether DISCO_PG_IAM_AUTH requests RDS IAM auth.
func iamAuthEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DISCO_PG_IAM_AUTH"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// iamBeforeConnect returns a pgx before-connect hook that mints a fresh RDS
// IAM auth token and installs it as the connection password, or (nil, nil)
// when IAM auth is disabled. The AWS region + credentials come from the
// ambient config (task role on ECS); the DB user + host are read from the
// per-dial ConnConfig. The DSN is expected to be passwordless.
func iamBeforeConnect(ctx context.Context) (func(context.Context, *pgx.ConnConfig) error, error) {
	if !iamAuthEnabled() {
		return nil, nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config for pg iam auth: %w", err)
	}
	region := awsCfg.Region
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	creds := awsCfg.Credentials
	return func(ctx context.Context, cc *pgx.ConnConfig) error {
		endpoint := net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port)))
		token, err := auth.BuildAuthToken(ctx, endpoint, region, cc.User, creds)
		if err != nil {
			return fmt.Errorf("build rds iam auth token: %w", err)
		}
		cc.Password = token
		return nil
	}, nil
}
