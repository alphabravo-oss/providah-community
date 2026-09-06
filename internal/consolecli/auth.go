package consolecli

import (
	"context"
	"errors"
	"regexp"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

// Prompt only after a password-authenticated MFA challenge. Never retry rejected credentials.
func authenticate(ctx context.Context, client providahv1connect.ConsoleServiceClient, input *pb.LoginRequest, readCode func() (string, error)) (*connect.Response[pb.SessionResponse], error) {
	defer func() { input.Password = ""; input.Code = "" }()
	session, err := client.Login(ctx, connect.NewRequest(input))
	if err != nil || !session.Msg.MfaRequired {
		return session, err
	}
	if readCode == nil || input.Code != "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authenticator code required"))
	}
	code, err := readCode()
	if err != nil {
		return nil, errors.New("unable to read authenticator code")
	}
	if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid authenticator code"))
	}
	input.Code = code
	session, err = client.Login(ctx, connect.NewRequest(input))
	if err == nil && session.Msg.MfaRequired {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authenticator code required"))
	}
	return session, err
}
