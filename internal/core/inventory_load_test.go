//go:build load

package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"filippo.io/age"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Explicit opt-in: creates and drops its own database, never starts provider workers.
func TestInventoryLoad(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL and run with -tags load")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	check := func(err error) {
		t.Helper()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
	}
	admin, err := pgxpool.New(ctx, dsn)
	check(err)
	defer admin.Close()
	name := "load_" + randomID()[:16]
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	check(err)
	defer func() {
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(dsn)
	check(err)
	u.Path = "/" + name
	pool, err := database.Open(ctx, u.String())
	check(err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `
 INSERT INTO organizations(id,name) SELECT repeat(md5('org'||n),2),'Organization '||n FROM generate_series(1,100)n;
 INSERT INTO users(id,email,password_hash,totp_ciphertext) SELECT repeat(md5('user'||n),2),'load-'||n||'@example.test','fake','fake' FROM generate_series(1,100)n;
 INSERT INTO memberships(org_id,user_id,permissions)
 SELECT DISTINCT repeat(md5('org'||o),2),repeat(md5('user'||n),2),ARRAY['resources.read']
 FROM generate_series(1,100)n CROSS JOIN LATERAL (VALUES(n),(1)) org(o);
 INSERT INTO sessions(id,user_id,refresh_hash,expires_at)
 SELECT repeat(md5('session'||n),2),repeat(md5('user'||n),2),decode(md5('refresh'||n),'hex'),now()+interval '1 hour' FROM generate_series(1,100)n;
 INSERT INTO connections(id,org_id,name,provider,region,key_id,ciphertext,enabled)
 SELECT repeat(md5('connection'||n),2),repeat(md5('org'||CASE WHEN n<=500 THEN 1 ELSE 2+(n-501)%99 END),2),'Connection '||n,
 CASE WHEN n%2=0 THEN 'hetzner' ELSE 'digitalocean' END,'global','fake','fake',false FROM generate_series(1,1000)n;
 INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status)
 SELECT repeat(md5(c.id||n),2),c.org_id,c.id,n::text,'node-'||lpad(n::text,5,'0'),c.provider,
 CASE WHEN n%2=0 THEN 'compute.server' ELSE 'storage.volume' END,'global',
 CASE WHEN n%3=0 THEN 'running' ELSE 'available' END FROM connections c CROSS JOIN generate_series(1,100)n;
 ANALYZE;
 `)
	check(err)
	identity, err := age.GenerateX25519Identity()
	check(err)
	svc, err := New(pool, Config{Origin: "http://load.test", BootstrapToken: randomID(), SessionKey: randomID(), AgeIdentity: identity.String()}, zerolog.New(io.Discard))
	check(err)
	server := httptest.NewServer(svc.Handler(""))
	defer server.Close()
	rows, err := pool.Query(ctx, "SELECT s.id,m.org_id FROM sessions s JOIN memberships m ON m.user_id=s.user_id WHERE m.org_id=repeat(md5('org'||substring((SELECT email FROM users WHERE id=s.user_id) from 'load-([0-9]+)@')),2) ORDER BY s.id")
	check(err)
	type operator struct {
		client      providahv1connect.ConsoleServiceClient
		org, detail string
	}
	operators := []operator{}
	for rows.Next() {
		var session, org string
		check(rows.Scan(&session, &org))
		h := http.Header{}
		check(svc.issue(h, session, "fake"))
		cookies := (&http.Response{Header: h}).Cookies()
		cookieHeader := ""
		for _, c := range cookies {
			if c.Name == "providah_access" {
				cookieHeader = c.Name + "=" + c.Value
			}
		}
		client := providahv1connect.NewConsoleServiceClient(server.Client(), server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Origin", "http://load.test")
				r.Header().Set("Cookie", cookieHeader)
				return next(ctx, r)
			}
		})))
		operators = append(operators, operator{client: client, org: org})
	}
	check(rows.Err())
	rows.Close()
	if len(operators) != 100 {
		t.Fatalf("operators=%d", len(operators))
	}
	var hot string
	check(pool.QueryRow(ctx, "SELECT repeat(md5('org1'),2)").Scan(&hot))
	t.Logf("profile: 100 organizations, 1000 connections, 100000 resources (50000 in hot org), 100 authenticated users; Go=%s %s/%s CPUs=%d pool=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), pool.Config().MaxConns)
	for _, concentrated := range []bool{false, true} {
		for i := range operators {
			op := &operators[i]
			org := op.org
			if concentrated {
				org = hot
			}
			first, e := op.client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, PageSize: 1}))
			check(e)
			if len(first.Msg.Resources) != 1 {
				t.Fatal("fixture missing resources")
			}
			op.detail = first.Msg.Resources[0].Id
		}
		for _, phase := range []string{"first-pass", "warm"} {
			type result struct {
				durations []time.Duration
				err       error
			}
			done := make(chan result, 100)
			start := make(chan struct{})
			began := time.Now()
			for _, op := range operators {
				go func() {
					<-start
					org := op.org
					if concentrated {
						org = hot
					}
					samples := []time.Duration{}
					token := ""
					for n := 0; n < 12; n++ {
						began := time.Now()
						var err error
						if n%4 == 3 {
							_, err = op.client.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: op.detail}))
						} else {
							req := &pb.ListResourcesRequest{OrganizationId: org, PageSize: 50}
							switch n % 4 {
							case 0:
								req.SortBy = "name"
							case 1:
								req.SortBy = "name"
								req.PageToken = token
							case 2:
								req.SortBy = "status"
								req.Provider = "hetzner"
								req.Kind = "compute.server"
								req.Search = "node"
							}
							response, e := op.client.ListResources(ctx, connect.NewRequest(req))
							err = e
							if err == nil {
								token = response.Msg.NextPageToken
								if len(response.Msg.Resources) == 0 {
									err = fmt.Errorf("unexpected empty inventory")
								}
							}
						}
						samples = append(samples, time.Since(began))
						if err != nil {
							done <- result{err: err}
							return
						}
					}
					done <- result{durations: samples}
				}()
			}
			close(start)
			samples := []time.Duration{}
			for range operators {
				r := <-done
				check(r.err)
				samples = append(samples, r.durations...)
			}
			elapsed := time.Since(began)
			slices.Sort(samples)
			p95 := samples[(len(samples)-1)*95/100]
			t.Logf("hot=%v phase=%s requests=%d p50=%s p95=%s p99=%s max=%s throughput=%.1f/s", concentrated, phase, len(samples), samples[len(samples)/2], p95, samples[(len(samples)-1)*99/100], samples[len(samples)-1], float64(len(samples))/elapsed.Seconds())
			if p95 >= time.Second {
				t.Errorf("stored-read p95 exceeds 1s: %s", p95)
			}
		}
	}
}
