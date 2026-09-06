package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/artifactstore"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/alphabravo-oss/providah-community/internal/core"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/alphabravo-oss/providah-community/internal/vaultstore"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

func Run(options Options) {
	if options.Edition == "" {
		options.Edition = "community"
	}
	if len(os.Args) > 1 && (os.Args[1] == "keygen" || os.Args[1] == "encryption-keygen") {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			panic(err)
		}
		if os.Args[1] == "encryption-keygen" {
			fmt.Println(id.String())
			return
		}
		token := func() string {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				panic(err)
			}
			return hex.EncodeToString(b)
		}
		fmt.Printf("AGE_IDENTITY=%s\nSESSION_KEY=%s\nBOOTSTRAP_TOKEN=%s\nDATABASE_URL=postgres://providah:providah@127.0.0.1:55432/providah?sslmode=disable\nORIGIN=http://localhost:5173\nCOOKIE_SECURE=false\nLISTEN_ADDR=127.0.0.1:8080\n", id.String(), token(), token())
		return
	}
	var output io.Writer = os.Stderr
	// Optional development file shipping; production keeps its normal stderr log shipper.
	if path := os.Getenv("DEV_LOG_FILE"); path != "" {
		file, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600) // #nosec G703 -- Log path is trusted deployment configuration, opened with private permissions.
		if e != nil {
			fmt.Fprintln(os.Stderr, "cannot open development log file")
			os.Exit(1)
		}
		_ = file.Close()
		rotated := &lumberjack.Logger{Filename: path, MaxSize: 10, MaxBackups: 2, MaxAge: 1}
		defer func() { _ = rotated.Close() }()
		output = io.MultiWriter(os.Stderr, rotated)
	}
	log := zerolog.New(output).With().Timestamp().Logger()
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	artifactMaintenance := mode == "check-artifacts" || mode == "backup-artifacts" || mode == "restore-artifacts"
	if (mode == "backup-artifacts" || mode == "restore-artifacts") && len(os.Args) != 3 {
		log.Fatal().Msg("usage: providah backup-artifacts|restore-artifacts DIRECTORY")
	}
	maintenance := mode == "check-encryption" || mode == "rotate-encryption" || mode == "seed-admin" || artifactMaintenance
	if mode != "serve" && !maintenance {
		log.Fatal().Msg("usage: providah [serve|keygen|encryption-keygen|check-encryption|rotate-encryption|check-artifacts|backup-artifacts DIRECTORY|restore-artifacts DIRECTORY]")
	}
	currentKey, err := readKeySetting("AGE_IDENTITY")
	if err != nil {
		log.Fatal().Msg("invalid AGE_IDENTITY configuration; use the value or its _FILE setting")
	}
	previousKeys, err := readKeySetting("AGE_PREVIOUS_IDENTITIES")
	if err != nil {
		log.Fatal().Msg("invalid AGE_PREVIOUS_IDENTITIES configuration; use the value or its _FILE setting")
	}
	cfg := core.Config{Edition: options.Edition, RuntimePolicy: options.RuntimePolicy, AutomationRuntimes: os.Getenv("AUTOMATION_RUNTIMES"), Origin: os.Getenv("ORIGIN"), BootstrapToken: os.Getenv("BOOTSTRAP_TOKEN"), SessionKey: os.Getenv("SESSION_KEY"), AgeIdentity: strings.TrimSpace(currentKey), AgePreviousIdentities: strings.Fields(previousKeys), SecureCookies: os.Getenv("COOKIE_SECURE") != "false"}
	if raw := os.Getenv("DATABASE_BUDGET_BYTES"); raw != "" {
		cfg.DatabaseBudgetBytes, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cfg.DatabaseBudgetBytes < 0 {
			log.Fatal().Msg("DATABASE_BUDGET_BYTES must be a nonnegative integer")
		}
	}
	if os.Getenv("EXTERNAL_VAULT_ADDR") != "" && !maintenance {
		token, e := readKeySetting("EXTERNAL_VAULT_TOKEN")
		if e != nil {
			log.Fatal().Msg("invalid external vault token configuration")
		}
		cfg.Vault = &vaultstore.Config{Address: os.Getenv("EXTERNAL_VAULT_ADDR"), Mount: os.Getenv("EXTERNAL_VAULT_MOUNT"), Namespace: os.Getenv("EXTERNAL_VAULT_NAMESPACE"), Token: strings.TrimSpace(token), CAFile: os.Getenv("EXTERNAL_VAULT_CA_FILE")}
	}
	if os.Getenv("OIDC_ISSUER") != "" && !maintenance {
		secret, e := readKeySetting("OIDC_CLIENT_SECRET")
		if e != nil {
			log.Fatal().Msg("invalid OIDC client secret configuration")
		}
		cfg.OIDC = &core.OIDCConfig{Issuer: os.Getenv("OIDC_ISSUER"), ClientID: os.Getenv("OIDC_CLIENT_ID"), ClientSecret: strings.TrimSpace(secret)}
	}
	cfg.DevelopmentCapture = os.Getenv("DEV_CAPTURE") == "true"
	if raw := os.Getenv("SMTP_CONFIG"); raw != "" && !maintenance {
		cfg.SMTP = new(notification.SMTP)
		if err := json.Unmarshal([]byte(raw), cfg.SMTP); err != nil {
			log.Fatal().Msg("SMTP_CONFIG must contain valid SMTP JSON")
		}
		validate := notification.Validate
		if cfg.DevelopmentCapture {
			validate = notification.ValidateDevelopment
		}
		if err := validate("email", cfg.SMTP.Sender, notification.Secret{SMTP: cfg.SMTP}); err != nil {
			log.Fatal().Msg("SMTP_CONFIG has invalid host, port, sender, or authentication settings")
		}
	}
	if socket := os.Getenv("LAUNCHER_SOCKET"); socket != "" && !maintenance {
		cfg.ProviderCall = provider.Client(socket)
		cfg.AutomationCall = automation.Client(socket)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if socket := os.Getenv("LAUNCHER_SOCKET"); socket != "" && !maintenance {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		for {
			runtimes, e := provider.LoadRuntimes(startup, socket)
			if e == nil {
				cfg.ProviderRuntimes = runtimes
				break
			}
			select {
			case <-startup.Done():
				cancel()
				log.Fatal().Msg("provider runtime catalog unavailable or incompatible")
			case <-time.After(250 * time.Millisecond):
			}
		}
		cancel()
	}
	if !maintenance || artifactMaintenance {
		raw, e := readKeySetting("ARTIFACT_STORE")
		if e != nil {
			log.Fatal().Msg("invalid ARTIFACT_STORE configuration")
		}
		if raw != "" {
			var storage artifactstore.Config
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&storage) != nil {
				log.Fatal().Msg("invalid ARTIFACT_STORE JSON")
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				log.Fatal().Msg("invalid ARTIFACT_STORE JSON")
			}
			local := cfg.DevelopmentCapture && !cfg.SecureCookies && notification.DevelopmentOrigin(cfg.Origin)
			cfg.Artifacts, e = artifactstore.New(storage, local)
			if e != nil {
				log.Fatal().Msg("invalid artifact storage settings")
			}
			startup, cancel := context.WithTimeout(ctx, 30*time.Second)
			e = cfg.Artifacts.Prepare(startup, local && !maintenance)
			cancel()
			if e != nil {
				log.Fatal().Msg("versioned artifact storage unavailable")
			}
		}
	}
	var pool *pgxpool.Pool
	if artifactMaintenance {
		pool, err = pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	} else {
		pool, err = database.OpenEdition(ctx, os.Getenv("DATABASE_URL"), options.Edition)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("database startup failed")
	}
	defer pool.Close()
	svc, err := core.New(pool, cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}
	if !maintenance {
		if err := svc.IndexStateOwnership(ctx); err != nil {
			log.Fatal().Err(err).Msg("state ownership indexing failed")
		}
		if err := svc.AuditRuntimeCatalog(ctx); err != nil {
			log.Fatal().Err(err).Msg("runtime catalog audit failed")
		}
	}
	if mode == "seed-admin" {
		if os.Getenv("DEV_SEED_ADMIN") != "true" {
			log.Fatal().Msg("admin seeding is disabled")
		}
		if e := svc.SeedAdministrator(ctx, os.Getenv("DEV_SEED_ADMIN_EMAIL")); e != nil {
			log.Fatal().Msg("administrator seed failed")
		}
		log.Info().Msg("administrator seed checked")
		return
	}
	if artifactMaintenance {
		var report any
		var e error
		switch mode {
		case "check-artifacts":
			report, e = svc.CheckArtifacts(ctx)
		case "backup-artifacts":
			report, e = svc.BackupArtifacts(ctx, os.Args[2])
		case "restore-artifacts":
			report, e = svc.RestoreArtifacts(ctx, os.Args[2])
		}
		if e != nil {
			log.Fatal().Err(e).Msg("artifact verification failed")
		}
		if json.NewEncoder(os.Stdout).Encode(report) != nil {
			log.Fatal().Msg("could not write artifact report")
		}
		return
	}
	if maintenance {
		report, e := svc.RewrapSecrets(ctx, mode == "rotate-encryption")
		if e != nil {
			log.Fatal().Err(e).Msg("encryption maintenance failed; no rotation committed")
		}
		if e = json.NewEncoder(os.Stdout).Encode(report); e != nil {
			log.Fatal().Msg("could not write maintenance report")
		}
		return
	}
	if endpoint := os.Getenv("TRACE_ENDPOINT"); endpoint != "" {
		ratio := 0.1
		if raw := os.Getenv("TRACE_SAMPLE_RATIO"); raw != "" {
			var e error
			ratio, e = strconv.ParseFloat(raw, 64)
			if e != nil {
				log.Fatal().Msg("invalid trace sampling ratio")
			}
		}
		shutdown, e := svc.ConfigureTracing(ctx, endpoint, ratio)
		if e != nil {
			log.Fatal().Msg("invalid trace exporter configuration")
		}
		defer func() {
			drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdown(drain) != nil {
				log.Error().Msg("trace shutdown failed")
			}
		}()
	}
	if metricsAddr := os.Getenv("METRICS_ADDR"); metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", svc.MetricsHandler())
		metrics := &http.Server{Addr: metricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
		listener, e := net.Listen("tcp", metricsAddr)
		if e != nil {
			log.Fatal().Msg("could not bind metrics listener")
		}
		go svc.StartTelemetry(ctx)
		go func() {
			if e := metrics.Serve(listener); e != nil && e != http.ErrServerClosed {
				log.Error().Msg("metrics listener stopped")
				stop()
			}
		}()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metrics.Shutdown(shutdown)
		}()
	}
	go svc.StartAutomationValidation(ctx)
	go svc.StartDiscovery(ctx)
	go svc.StartOperations(ctx)
	go svc.StartScheduler(ctx)
	go svc.StartNotifications(ctx)
	go svc.StartAuditExport(ctx)
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{Addr: addr, Handler: svc.Handler(os.Getenv("STATIC_DIR")), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Info().Str("address", addr).Msg("Providah started")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server stopped")
	}
}

// Key material is injected as an environment value or a mounted file, never a CLI argument.
func readKeySetting(name string) (string, error) {
	value, path := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && path != "" {
		return "", errors.New("conflicting secret settings")
	}
	if path != "" {
		f, err := os.Open(path) // #nosec G703 -- Key file path is trusted deployment configuration; file type and read size are checked.
		if err != nil {
			return "", errors.New("cannot read key file")
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("key file must be a regular file")
		}
		b, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(b) > 16384 {
			return "", errors.New("invalid key file")
		}
		value = string(b)
	}
	if len(value) > 16384 {
		return "", errors.New("key setting too large")
	}
	return value, nil
}
