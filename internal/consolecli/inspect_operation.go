package consolecli

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

// Each read rechecks server authorization. Waiting never retries a failed request or submits work.
func inspectOperation(ctx context.Context, client providahv1connect.ConsoleServiceClient, org, id string, wait time.Duration) (*pb.OperationResponse, error) {
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	for {
		result, err := client.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{OrganizationId: org, Id: id}))
		if err != nil {
			return nil, err
		}
		if result.Msg.Operation == nil || result.Msg.Operation.Id != id {
			return nil, errors.New("invalid operation response")
		}
		pending := false
		switch result.Msg.Operation.Status {
		case pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL, pb.OperationStatus_OPERATION_STATUS_QUEUED, pb.OperationStatus_OPERATION_STATUS_DISPATCHING, pb.OperationStatus_OPERATION_STATUS_OBSERVING:
			pending = true
		case pb.OperationStatus_OPERATION_STATUS_SUCCEEDED, pb.OperationStatus_OPERATION_STATUS_FAILED, pb.OperationStatus_OPERATION_STATUS_UNCERTAIN, pb.OperationStatus_OPERATION_STATUS_REJECTED, pb.OperationStatus_OPERATION_STATUS_CANCELED, pb.OperationStatus_OPERATION_STATUS_EXPIRED, pb.OperationStatus_OPERATION_STATUS_RESOLVED:
		default:
			return nil, errors.New("unknown operation status")
		}
		if wait == 0 || !pending {
			return result.Msg, nil
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
