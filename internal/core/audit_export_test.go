package core

import (
	"bytes"
	"compress/gzip"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"io"
	"strings"
	"testing"
)

func TestAuditArchive(t *testing.T) {
	rows := []database.AuditEvent{{ID: 7, OrgID: "org", Actor: "owner", Action: "operation.failed", Target: "node", Details: []byte(`{"status":"failed","credential":"never-export","provider_error":{"secret":"never-export"},"reason":{"nested":"never-export"},"permissions":["audit.read"]}`)}}
	payload, err := auditArchive("org", rows, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := auditArchive("org", rows, "")
	if err != nil || !bytes.Equal(payload, again) {
		t.Fatal("archive not deterministic", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "never-export") || !strings.Contains(text, `"first_event_id":"7"`) || !strings.Contains(text, `"permissions":["audit.read"]`) || strings.Count(text, "\n") != 2 {
		t.Fatal("unsafe or invalid archive", text)
	}
	if _, err = auditArchive("other", rows, ""); err == nil {
		t.Fatal("cross-organization archive accepted")
	}
	rows[0].Details = []byte(`{"reason":"` + strings.Repeat("x", 4<<20) + `"}`)
	if _, err = auditArchive("org", rows, ""); err == nil {
		t.Fatal("unbounded archive accepted")
	}
}
