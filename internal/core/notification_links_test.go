package core

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestNotificationRecordLinks(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, tc := range []struct{ kind, path, key string }{
		{"operation.failed", "/app/operations", "operation"},
		{"approval.requested", "/app/operations", "operation"},
		{"automation.validation_failed", "/app/templates", "validation"},
		{"automation.version_revoked", "/app/templates", "version"},
		{"schedule.skipped", "/app/schedules", "schedule"},
		{"notification.verification", "/admin/notifications", "destination"},
		{"connection.refresh_failed", "/admin/connections", "connection"},
		{"provider.runtime_changed", "/admin/modules", ""},
		{"audit.export_failed", "/admin/audit-export", ""},
		{"account.password_changed", "/app", ""},
	} {
		var payload map[string]any
		if err := json.Unmarshal(notificationPayload(id, "org&other=wrong", tc.kind, id), &payload); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(payload["console_path"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if u.Path != tc.path || u.Query().Get("org") != "org&other=wrong" || u.Query().Get("other") != "" {
			t.Fatal("wrong scoped path", u)
		}
		if tc.key != "" && u.Query().Get(tc.key) != id {
			t.Fatal("missing record", u)
		}
		if len(payload) != 6 {
			t.Fatal("unexpected payload fields", payload)
		}
	}
	if strings.Contains(notificationConsolePath(id, "operation.failed", "secret target"), "secret") {
		t.Fatal("unsafe target included")
	}
}
