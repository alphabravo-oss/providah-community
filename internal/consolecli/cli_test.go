package consolecli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

const loginInput = `{"email":"cli@example.test","password":"private-password","code":"123456"}`

type service struct {
	logins int
	providahv1connect.UnimplementedConsoleServiceHandler
	t              *testing.T
	origin         string
	reads, logouts int
	failure        error
	loginFailure   bool
	optionalCode   bool
	challenge      bool
}

func (s *service) Login(_ context.Context, r *connect.Request[pb.LoginRequest]) (*connect.Response[pb.SessionResponse], error) {
	s.logins++
	if r.Header().Get("Origin") != s.origin || r.Msg.Email != "cli@example.test" || r.Msg.Password != "private-password" || (r.Msg.Code != "123456" && (!s.optionalCode || r.Msg.Code != "")) {
		s.t.Fatal("login input/origin changed")
	}
	if s.loginFailure {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("private-password"))
	}
	if s.challenge && r.Msg.Code == "" {
		return connect.NewResponse(&pb.SessionResponse{MfaRequired: true}), nil
	}
	response := connect.NewResponse(&pb.SessionResponse{})
	response.Header().Add("Set-Cookie", (&http.Cookie{Name: "providah_access", Value: "private-session", Path: "/api", HttpOnly: true}).String())
	return response, nil
}
func (s *service) ListResources(_ context.Context, r *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	s.reads++
	if r.Header().Get("Origin") != s.origin || !strings.Contains(r.Header().Get("Cookie"), "providah_access=private-session") || r.Msg.OrganizationId != "org" || r.Msg.ConnectionId != "connection" || r.Msg.Kind != "compute.server" || r.Msg.Search != "node" || r.Msg.PageToken != "cursor" || r.Msg.PageSize != 200 {
		s.t.Fatal("session or scoped query lost")
	}
	if s.failure != nil {
		return nil, s.failure
	}
	return connect.NewResponse(&pb.ListResourcesResponse{Resources: []*pb.Resource{{Id: "r1", Name: "node\x1b[2J\nspoof", Provider: "aws", Kind: "compute.server", Status: "running", Size: "t3.small"}}, NextPageToken: "next-cursor"}), nil
}
func (s *service) Logout(_ context.Context, r *connect.Request[pb.LogoutRequest]) (*connect.Response[pb.LogoutResponse], error) {
	if !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("logout missing session")
	}
	s.logouts++
	return connect.NewResponse(&pb.LogoutResponse{}), nil
}
func TestCLI(t *testing.T) {
	for _, mode := range []string{"table", "json", "denied", "login failure", "optional MFA", "missing MFA"} {
		t.Run(mode, func(t *testing.T) {
			s := &service{t: t}
			path, handler := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", handler))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			args := []string{"resources", "--url", server.URL, "--org", "org", "--connection", "connection", "--kind", "compute.server", "--search", "node", "--page", "cursor", "--auth-stdin"}
			if mode == "json" {
				args = append(args, "--json")
			}
			if mode == "denied" {
				s.failure = connect.NewError(connect.CodePermissionDenied, errors.New("private-password"))
			}
			s.loginFailure = mode == "login failure"
			var out, diagnostics bytes.Buffer
			input := loginInput
			if mode == "optional MFA" || mode == "missing MFA" {
				s.optionalCode = true
				s.challenge = mode == "missing MFA"
				input = `{"email":"cli@example.test","password":"private-password"}`
			}
			code := Run(context.Background(), args, strings.NewReader(input), &out, &diagnostics)
			want := 0
			if mode == "denied" {
				want = 4
			}
			if mode == "login failure" || mode == "missing MFA" {
				want = 3
			}
			if code != want {
				t.Fatalf("exit %d: %s", code, diagnostics.String())
			}
			text := out.String() + diagnostics.String()
			for _, secret := range []string{"private-password", "private-session", "123456", "\x1b"} {
				if strings.Contains(text, secret) {
					t.Fatal("secret or terminal control leaked")
				}
			}
			if mode == "login failure" || mode == "missing MFA" {
				if s.reads != 0 || s.logouts != 0 {
					t.Fatal("unauthenticated reads")
				}
			} else if s.reads != 1 || s.logouts != 1 {
				t.Fatal("missing read or cleanup")
			}
			if want == 0 && (!strings.Contains(out.String(), "next-cursor") || !strings.Contains(out.String(), "t3.small")) {
				t.Fatal("missing data/pagination")
			}
			if mode == "json" && !strings.Contains(out.String(), `"nextPageToken"`) {
				t.Fatal("not protobuf JSON")
			}
		})
	}
	for _, address := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/path", "https://example.com?secret=x", "file:///tmp/test", "https://example.com/#x"} {
		if _, e := endpoint(address); e == nil {
			t.Fatalf("unsafe endpoint accepted: %s", address)
		}
	}
	for _, address := range []string{"https://example.com", "http://localhost:8760", "http://127.0.0.1:8760", "http://[::1]:8760"} {
		if _, e := endpoint(address); e != nil {
			t.Fatal(e)
		}
	}
	for _, input := range []string{loginInput + "{}", `{"email":"x","password":"x","code":"123456","token":"unexpected"}`, strings.Repeat("x", 4097)} {
		var output bytes.Buffer
		if code := Run(context.Background(), []string{"session", "--url", "http://127.0.0.1:1", "--auth-stdin"}, strings.NewReader(input), &output, &output); code != 2 {
			t.Fatal("bad authentication input accepted")
		}
	}
}
func TestRedirectRefused(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	var output bytes.Buffer
	if Run(context.Background(), []string{"session", "--url", redirect.URL, "--auth-stdin"}, strings.NewReader(loginInput), &output, &output) == 0 || called {
		t.Fatal("credentials followed redirect")
	}
}

func TestInteractiveMFAChallenge(t *testing.T) {
	for _, mode := range []string{"optional", "challenge", "denied", "bad-code", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			s := &service{t: t, optionalCode: true, challenge: mode != "optional", loginFailure: mode == "denied"}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle(path, h)
			server := httptest.NewServer(mux)
			defer server.Close()
			client := providahv1connect.NewConsoleServiceClient(server.Client(), server.URL)
			prompts := 0
			input := &pb.LoginRequest{Email: "cli@example.test", Password: "private-password"}
			session, err := authenticate(context.Background(), client, input, func() (string, error) {
				prompts++
				if mode == "bad-code" {
					return "wrong", nil
				}
				if mode == "read-error" {
					return "", errors.New("private detail")
				}
				return "123456", nil
			})
			success := mode == "optional" || mode == "challenge"
			if (err == nil) != success || success && (session == nil || session.Msg.MfaRequired) {
				t.Fatal("unexpected authentication outcome", err)
			}
			wantPrompts, wantLogins := 1, 1
			if mode == "optional" || mode == "denied" {
				wantPrompts = 0
			}
			if mode == "challenge" {
				wantLogins = 2
			}
			if prompts != wantPrompts || s.logins != wantLogins || input.Password != "" || input.Code != "" {
				t.Fatal("prompt, retry or credential cleanup mismatch")
			}
		})
	}
}
