package awsauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type Credential struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
	RoleARN         string `json:"role_arn,omitempty"`
	ExternalID      string `json:"external_id,omitempty"`
	AccountID       string `json:"account_id,omitempty"`
}

var accountPattern = regexp.MustCompile(`^[0-9]{12}$`)
var externalPattern = regexp.MustCompile(`^[A-Za-z0-9_+=,.@:/-]+$`)
var rolePattern = regexp.MustCompile(`^role/[A-Za-z0-9_+=,.@/-]+$`)
var errInvalid = errors.New("invalid AWS credential configuration")

func Parse(raw string) (Credential, error) {
	var c Credential
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 16384 || d.Decode(&c) != nil || d.Decode(&struct{}{}) != io.EOF || c.AccessKeyID == "" || len(c.AccessKeyID) > 256 || c.SecretAccessKey == "" || len(c.SecretAccessKey) > 4096 || len(c.SessionToken) > 8192 {
		return Credential{}, errInvalid
	}
	if c.AccountID != "" && !accountPattern.MatchString(c.AccountID) || c.ExternalID != "" && (c.RoleARN == "" || len(c.ExternalID) < 2 || len(c.ExternalID) > 1224 || !externalPattern.MatchString(c.ExternalID)) {
		return Credential{}, errInvalid
	}
	if c.RoleARN != "" {
		a, e := arn.Parse(c.RoleARN)
		if e != nil || len(c.RoleARN) > 2048 || a.Service != "iam" || a.Region != "" || !accountPattern.MatchString(a.AccountID) || !rolePattern.MatchString(a.Resource) || (a.Partition != "aws" && a.Partition != "aws-cn" && a.Partition != "aws-us-gov") || (c.AccountID != "" && c.AccountID != a.AccountID) {
			return Credential{}, errInvalid
		}
	}
	return c, nil
}

// Broker never falls back to source credentials when role assumption or account verification fails.
// Its output is deliberately compatible with provider runtimes that understand only static credentials.
func Broker(ctx context.Context, raw, region, session string, client *http.Client) (string, error) {
	c, err := Parse(raw)
	if err != nil {
		return "", err
	}
	cfg := aws.Config{Region: region, HTTPClient: client, RetryMaxAttempts: 1, Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken))}
	expected := c.AccountID
	if c.RoleARN != "" {
		a, _ := arn.Parse(c.RoleARN)
		partition := "aws"
		if strings.HasPrefix(region, "cn-") {
			partition = "aws-cn"
		} else if strings.HasPrefix(region, "us-gov-") {
			partition = "aws-us-gov"
		}
		if a.Partition != partition {
			return "", errInvalid
		}
		expected = a.AccountID
		cfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), c.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = session
			o.Duration = 15 * time.Minute
			if c.ExternalID != "" {
				o.ExternalID = aws.String(c.ExternalID)
			}
		}))
	}
	if expected != "" {
		identity, e := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if e != nil || aws.ToString(identity.Account) != expected {
			return "", errors.New("AWS account verification failed")
		}
	}
	v, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return "", errors.New("AWS credential exchange failed")
	}
	b, err := json.Marshal(Credential{AccessKeyID: v.AccessKeyID, SecretAccessKey: v.SecretAccessKey, SessionToken: v.SessionToken})
	return string(b), err
}
