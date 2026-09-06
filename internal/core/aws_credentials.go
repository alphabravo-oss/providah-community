package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/awsauth"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/alphabravo-oss/providah-community/internal/vaultstore"
	"strings"
	"time"
)

func (s *Service) providerCredential(ctx context.Context, cloud, raw, region, org, connection string) (string, error) {
	ref, err := vaultstore.Parse(raw)
	if err != nil {
		return "", err
	}
	if ref != nil {
		raw, err = s.vault.Resolve(ctx, org, cloud, *ref)
		if err != nil {
			return "", err
		}
		if err = validateCredential(cloud, raw); err != nil {
			return "", fmt.Errorf("external credential has invalid provider format")
		}
	}
	if cloud != "aws" {
		return raw, nil
	}
	client := s.cfg.AWSHTTPClient
	if client == nil {
		var err error
		client, err = notification.PublicHTTPSClient(s.cfg.Origin)
		if err != nil {
			return "", err
		}
		defer client.CloseIdleConnections()
	}
	// ponytail: hold the connection lock during bounded STS calls; split and recheck revisions if contention grows.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id := sha256.Sum256([]byte(org + ":" + connection))
	return awsauth.Broker(ctx, raw, region, fmt.Sprintf("providah-%x", id[:16]), client)
}

func (s *Service) prepareCredential(cloud, raw string) (string, error) {
	if err := validateCredential(cloud, raw); err != nil {
		return "", err
	}
	ref, err := vaultstore.Parse(raw)
	if err != nil {
		return "", err
	}
	if ref == nil {
		return raw, nil
	}
	if s.vault == nil {
		return "", conflict("An external secret store must be configured before saving a reference.")
	}
	return s.vault.Bind(*ref), nil
}

func credentialSource(raw string) string {
	if strings.HasPrefix(raw, vaultstore.Prefix) {
		return "vault_kv2"
	}
	return "builtin"
}
