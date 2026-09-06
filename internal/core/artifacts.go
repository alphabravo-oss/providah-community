package core

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/alphabravo-oss/providah-community/internal/artifactstore"
)

type artifactEnvelope struct {
	Reference artifactstore.Reference
	Identity  string
}

func (s *Service) sealArtifact(ctx context.Context, key string, raw []byte) ([]byte, string, error) {
	if s.cfg.Artifacts == nil {
		b, e := s.seal(string(raw))
		return b, "database", e
	}
	identity, e := age.GenerateX25519Identity()
	if e != nil {
		return nil, "", e
	}
	encrypted, e := encryptSecret(string(raw), identity.Recipient())
	if e != nil {
		return nil, "", e
	}
	ref, e := s.cfg.Artifacts.Put(ctx, key, encrypted)
	if e != nil {
		return nil, "", e
	}
	envelope, e := json.Marshal(artifactEnvelope{Reference: ref, Identity: identity.String()})
	if e != nil {
		return nil, "", e
	}
	cipher, e := s.seal(string(envelope))
	return cipher, "s3", e
}
func (s *Service) openArtifact(ctx context.Context, key, storage string, cipher []byte, limit int64) (string, error) {
	if storage == "database" {
		return s.openBounded(cipher, limit)
	}
	if storage != "s3" || s.cfg.Artifacts == nil {
		return "", errors.New("protected artifact storage unavailable")
	}
	raw, e := s.open(cipher)
	if e != nil {
		return "", e
	}
	var envelope artifactEnvelope
	if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.Reference.Key != key {
		return "", errors.New("invalid protected artifact envelope")
	}
	identity, e := age.ParseX25519Identity(envelope.Identity)
	if e != nil {
		return "", errors.New("invalid protected artifact key")
	}
	encrypted, e := s.cfg.Artifacts.Read(ctx, envelope.Reference)
	if e != nil {
		return "", e
	}
	return decryptBounded(encrypted, limit, identity)
}
