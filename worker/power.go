package worker

import (
	"context"
	"net/http"

	"github.com/alphabravo-oss/providah-community/internal/providers"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func Power(ctx context.Context, r provider.Request, client *http.Client) provider.Response {
	return providers.Power(ctx, r, client)
}

func AWSClient(r provider.Request, client *http.Client, retries int) (*ec2.Client, error) {
	return providers.AWSClient(r, client, retries)
}

func ReadFailure(phase string) provider.PowerResult { return providers.PowerReadFailure(phase) }
func StateMismatch(p *provider.PowerRequest, status string) bool {
	return providers.PowerStateMismatch(p, status)
}
func Observed(p *provider.PowerRequest, status, actionStatus string) provider.PowerResult {
	return providers.PowerObserved(p, status, actionStatus)
}
func DeletionResult(p *provider.PowerRequest, status string, lines []string, remove func() error) *provider.PowerResult {
	return providers.StorageDeletionResult(p, status, lines, remove)
}
