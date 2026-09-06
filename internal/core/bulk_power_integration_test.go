//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"testing"
	"time"
)

func exerciseBulkPower(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org string) {
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var connection string
	check(s.pool.QueryRow(ctx, "SELECT id FROM connections WHERE org_id=$1 AND provider='hetzner' AND enabled AND deleted_at IS NULL LIMIT 1", org).Scan(&connection))
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous }()
	calls := 0
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		calls++
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "succeeded", Status: "running"}}, nil
	}
	ids := []string{randomID(), randomID(), randomID(), randomID()}
	for i, id := range ids {
		kind := "compute.server"
		if i == 3 {
			kind = "storage.volume"
		}
		check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: connection, Provider: "hetzner", Kind: kind, NativeID: fmt.Sprint(7800 + i), Name: fmt.Sprintf("bulk-%d", i), Region: "fsn1", Status: "off"}))
	}
	// A real foreign-tenant resource must be indistinguishable from an unknown ID.
	foreignOrg, foreignConnection, foreignResource := randomID(), randomID(), randomID()
	check(s.q.CreateOrganization(ctx, database.CreateOrganizationParams{ID: foreignOrg, Name: "Bulk isolation tenant"}))
	_, err := s.pool.Exec(ctx, "INSERT INTO connections(id,org_id,name,provider,region,key_id,ciphertext) SELECT $1,$2,'Foreign secret account',provider,region,key_id,ciphertext FROM connections WHERE id=$3", foreignConnection, foreignOrg, connection)
	check(err)
	check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: foreignResource, OrgID: foreignOrg, ConnectionID: foreignConnection, Provider: "hetzner", Kind: "compute.server", NativeID: "7899", Name: "Foreign secret server", Region: "fsn1", Status: "off"}))
	missing := randomID()
	preview, err := client.PreviewBulkPower(ctx, connect.NewRequest(&pb.PreviewBulkPowerRequest{OrganizationId: org, ResourceIds: append(append([]string{}, ids...), foreignResource, missing), Action: "start"}))
	check(err)
	if len(preview.Msg.Targets) != 6 || preview.Msg.Targets[3].Eligibility != "unsupported" || preview.Msg.Targets[4].Eligibility != "inaccessible" || preview.Msg.Targets[4].Name != "" || preview.Msg.Targets[4].NativeId != "" {
		t.Fatal("invalid eligibility or inaccessible metadata", preview.Msg)
	}
	if preview.Msg.Targets[4].Eligibility != preview.Msg.Targets[5].Eligibility || preview.Msg.Targets[4].Detail != preview.Msg.Targets[5].Detail {
		t.Fatal("foreign existence leaked")
	}
	tokens := []string{preview.Msg.Targets[0].ReviewToken, preview.Msg.Targets[1].ReviewToken}
	if tokens[0] == "" || tokens[1] == "" {
		t.Fatal("eligible targets lack review")
	}
	_, err = client.PreviewBulkPower(ctx, connect.NewRequest(&pb.PreviewBulkPowerRequest{OrganizationId: org, ResourceIds: []string{ids[0], ids[0]}, Action: "start"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("duplicate targets accepted")
	}
	request := &pb.RequestBulkPowerRequest{OrganizationId: org, ReviewTokens: []string{tokens[0], tokens[0]}, Reason: "Start the reviewed application servers"}
	_, err = client.RequestBulkPower(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("duplicate reviews accepted")
	}
	request.ReviewTokens = []string{tokens[0], tokens[1] + "tampered"}
	_, err = client.RequestBulkPower(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("tampered review accepted")
	}
	var count int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM operations WHERE resource_id=ANY($1)", ids).Scan(&count))
	if count != 0 {
		t.Fatal("invalid envelope partially wrote intent")
	}
	_, err = s.pool.Exec(ctx, "UPDATE resources SET status='running' WHERE id=$1", ids[1])
	check(err)
	request.ReviewTokens = tokens
	result, err := client.RequestBulkPower(ctx, connect.NewRequest(request))
	check(err)
	if len(result.Msg.Results) != 2 || result.Msg.Results[0].Operation == nil || result.Msg.Results[1].Error == "" || calls != 0 {
		t.Fatal("partial request not represented", result.Msg)
	}
	first := result.Msg.Results[0].Operation.Id
	_, err = s.pool.Exec(ctx, "UPDATE resources SET status='off' WHERE id=$1", ids[1])
	check(err)
	retried, err := client.RequestBulkPower(ctx, connect.NewRequest(request))
	check(err)
	if retried.Msg.Results[0].Operation.Id != first || retried.Msg.Results[1].Operation == nil {
		t.Fatal("retry duplicated or lost successful intent")
	}
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM operations WHERE resource_id=ANY($1)", ids).Scan(&count))
	if count != 2 {
		t.Fatal("unexpected operation count", count)
	}
	request.Reason = "Changed input must not reuse a review key"
	changed, err := client.RequestBulkPower(ctx, connect.NewRequest(request))
	check(err)
	for _, item := range changed.Msg.Results {
		if item.Operation != nil || item.Error == "" {
			t.Fatal("reason changed under same key")
		}
	}
	for range 2 {
		job, e := s.q.ClaimOperation(ctx)
		check(e)
		check(s.runOperation(ctx, job))
	}
	if calls != 2 {
		t.Fatal("bulk did not use existing executor")
	}
	// Credential changes invalidate unsubmitted review, even when server state is unchanged.
	_, err = s.pool.Exec(ctx, "UPDATE connections SET revision=revision+1 WHERE id=$1", connection)
	check(err)
	stale, err := client.RequestBulkPower(ctx, connect.NewRequest(&pb.RequestBulkPowerRequest{OrganizationId: org, ReviewTokens: []string{preview.Msg.Targets[2].ReviewToken}, Reason: "Test reviewed credential revision"}))
	check(err)
	if stale.Msg.Results[0].Operation != nil || !strings.Contains(stale.Msg.Results[0].Error, "changed") {
		t.Fatal("stale credential accepted", stale.Msg)
	}
	// A completed operation schedules discovery; publish the subsequent inventory observation.
	_, err = s.pool.Exec(ctx, "UPDATE resources SET status='running',observed_at=now() WHERE id=$1", ids[0])
	check(err)
	shutdown, err := client.PreviewBulkPower(ctx, connect.NewRequest(&pb.PreviewBulkPowerRequest{OrganizationId: org, ResourceIds: []string{ids[0]}, Action: "shutdown"}))
	check(err)
	if shutdown.Msg.Targets[0].ReviewToken == "" {
		t.Fatal("shutdown not eligible", shutdown.Msg.Targets[0])
	}
	stopped, err := client.RequestBulkPower(ctx, connect.NewRequest(&pb.RequestBulkPowerRequest{OrganizationId: org, ReviewTokens: []string{shutdown.Msg.Targets[0].ReviewToken}, Reason: "Stop the reviewed server"}))
	check(err)
	op := stopped.Msg.Results[0].Operation
	if op == nil || op.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL {
		t.Fatal("bulk bypassed independent approval")
	}
	_, err = client.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: op.Id, Approve: true, Reason: "Self approval must fail"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("bulk self approval accepted")
	}
	_, err = client.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: op.Id}))
	check(err)
	// A correctly signed token from another audience or an expired review must fail before intent.
	for _, audience := range []string{"providah-console", "providah-bulk-power"} {
		proof := powerReview{}
		_, e := jwt.ParseWithClaims(shutdown.Msg.Targets[0].ReviewToken, &proof, func(*jwt.Token) (any, error) { return []byte(s.cfg.SessionKey), nil })
		check(e)
		proof.Audience = jwt.ClaimStrings{audience}
		if audience == "providah-bulk-power" {
			proof.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
		}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, proof).SignedString([]byte(s.cfg.SessionKey))
		check(e)
		_, e = client.RequestBulkPower(ctx, connect.NewRequest(&pb.RequestBulkPowerRequest{OrganizationId: org, ReviewTokens: []string{raw}, Reason: "Reject expired or wrong audience"}))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("invalid proof accepted")
		}
	}
}
