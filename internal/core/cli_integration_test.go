//go:build integration

package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/consolecli"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"golang.org/x/crypto/bcrypt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exerciseCLI(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org, user := randomID(), randomID()
	email := user + "@example.test"
	password := "cli-fixture-password"
	secret := "JBSWY3DPEHPK3PXP"
	ciphertext, e := s.seal(secret)
	check(e)
	hashed, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO organizations(id,name) VALUES($1,'CLI isolation')", org)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext) VALUES($1,$2,$3,$4)", user, email, hashed, ciphertext)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO memberships(org_id,user_id,permissions) VALUES($1,$2,ARRAY['resources.read'])", org, user)
	check(e)
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	oldOrigin := s.cfg.Origin
	s.cfg.Origin = server.URL
	defer func() { s.cfg.Origin = oldOrigin }()
	run := func(command, organization string, want int, extra ...string) []byte {
		// Reset test authentication limits and this disposable user's replay counter between logins.
		_, e := s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key LIKE 'auth-peer:%' OR key=$1", "login:"+hex.EncodeToString(hash(email)))
		check(e)
		_, e = s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE id=$1", user)
		check(e)
		code, e := integrationTOTP(secret)
		check(e)
		data, e := json.Marshal(map[string]string{"email": email, "password": password, "code": code})
		check(e)
		var out, diagnostics bytes.Buffer
		args := []string{command, "--url", server.URL, "--org", organization, "--auth-stdin", "--json"}
		args = append(args, extra...)
		exit := consolecli.Run(ctx, args, bytes.NewReader(data), &out, &diagnostics)
		if exit != want {
			t.Fatalf("CLI exit=%d want=%d: %s", exit, want, diagnostics.String())
		}
		if strings.Contains(out.String()+diagnostics.String(), password) || strings.Contains(out.String()+diagnostics.String(), code) {
			t.Fatal("CLI leaked authentication")
		}
		var count int
		check(s.pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", user).Scan(&count))
		if count != 0 {
			t.Fatal("CLI session was not revoked")
		}
		if want == 0 && !json.Valid(out.Bytes()) {
			t.Fatal("CLI did not emit JSON")
		}
		return out.Bytes()
	}
	run("resources", org, 0)
	run("resources", randomID(), 4)
	run("connections", org, 4)
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET permissions='{}' WHERE org_id=$1 AND user_id=$2", org, user)
	check(e)
	run("resources", org, 4)
	// Operation requests remain on the API's independent-approval path; no worker is run.
	oldProvider := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = oldProvider }()
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		t.Fatal("CLI test reached a cloud worker")
		return provider.Response{}, nil
	}
	connection, resource := randomID(), randomID()
	cloudCipher, e := s.seal("cli-fake-cloud-token")
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO connections(id,org_id,name,provider,region,key_id,ciphertext) VALUES($1,$2,'CLI fixture','hetzner','fsn1','fixture',$3)", connection, org, cloudCipher)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size) VALUES($1,$2,$3,'123','CLI server','hetzner','compute.server','fsn1','off','cx23')", resource, org, connection)
	check(e)
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET permissions=ARRAY['resources.read','operations.request','operations.approve','operations.read'] WHERE org_id=$1 AND user_id=$2", org, user)
	check(e)
	file := filepath.Join(t.TempDir(), "operation.json")
	write := func(v any) { data, e := json.Marshal(v); check(e); check(os.WriteFile(file, data, 0600)) }
	write(map[string]any{"resourceId": resource, "action": "resize", "expectedStatus": "off", "expectedSize": "cx23", "targetSize": "cx33", "reason": "CLI resize fixture", "idempotencyKey": randomID()})
	response := run("request-operation", org, 0, "--input", file)
	var created struct {
		Operation struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"operation"`
	}
	check(json.Unmarshal(response, &created))
	if created.Operation.ID == "" || created.Operation.Status != "OPERATION_STATUS_AWAITING_APPROVAL" {
		t.Fatal("CLI bypassed approval")
	}
	var same struct {
		Operation struct {
			ID string `json:"id"`
		} `json:"operation"`
	}
	check(json.Unmarshal(run("request-operation", org, 0, "--input", file), &same))
	if same.Operation.ID != created.Operation.ID {
		t.Fatal("CLI duplicated request")
	}
	write(map[string]any{"id": created.Operation.ID, "approve": true, "reason": "CLI independent review"})
	run("review-operation", org, 4, "--input", file)
	requester, requesterEmail := user, email
	user = randomID()
	email = user + "@example.test"
	_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext) VALUES($1,$2,$3,$4)", user, email, hashed, ciphertext)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO memberships(org_id,user_id,permissions) VALUES($1,$2,ARRAY['resources.read','operations.approve'])", org, user)
	check(e)
	run("review-operation", org, 0, "--input", file)
	user, email = requester, requesterEmail
	run("cancel-operation", org, 0, "--id", created.Operation.ID)
	var status string
	check(s.pool.QueryRow(ctx, "SELECT status FROM operations WHERE id=$1", created.Operation.ID).Scan(&status))
	if status != "canceled" {
		t.Fatal("CLI cancellation not persisted")
	}

	_, e = s.pool.Exec(ctx, "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size) SELECT md5(n::text)||md5(n::text),$1,$2,(1000+n)::text,'cli-batch-'||n,'hetzner','compute.server','fsn1','off','cx23' FROM generate_series(1,401)n", org, connection)
	check(e)
	var inventory struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
		Next string `json:"nextPageToken"`
	}
	check(json.Unmarshal(run("resources", org, 0, "--all", "--search", "cli-batch-", "--connection", connection), &inventory))
	if len(inventory.Resources) != 401 || inventory.Next != "" {
		t.Fatal("CLI failed to collect the full scoped inventory")
	}
	seen := map[string]bool{}
	for _, r := range inventory.Resources {
		if seen[r.ID] {
			t.Fatal("CLI duplicated a paginated resource")
		}
		seen[r.ID] = true
	}

}
