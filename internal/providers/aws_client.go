package providers

import (
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/awsauth"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"net/http"
)

func AWSClient(r provider.Request, client *http.Client, retries int) (*ec2.Client, error) {
	secret, err := awsauth.Parse(r.Credential)
	if err != nil || secret.RoleARN != "" || secret.AccountID != "" || secret.ExternalID != "" {
		return nil, errors.New("invalid credential: broker required")
	}

	svc := ec2.NewFromConfig(aws.Config{Region: r.Region, Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(secret.AccessKeyID, secret.SecretAccessKey, secret.SessionToken)), HTTPClient: client, RetryMaxAttempts: retries})
	return svc, nil
}
