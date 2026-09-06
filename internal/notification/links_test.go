package notification

import (
	"encoding/json"
	"testing"
)

func TestConsoleLink(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/app/operations?org=org&operation=op", "/app/operations?org=org&operation=op"},
		{"/admin/notifications?org=org&destination=dest", "/admin/notifications?org=org&destination=dest"},
		{"/", "/"}, {"https://evil.test/", "/"}, {"//evil.test/", "/"},
		{"/\\evil.test/", "/"}, {"/app/../admin", "/"}, {"/unknown", "/"},
		{"/app\r\nOther: text", "/"}, {"/app#fragment", "/"},
	} {
		b, _ := json.Marshal(map[string]string{"console_path": tc.path})
		if got := consoleLink("https://console.test/", b); got != "https://console.test"+tc.want {
			t.Fatalf("%q: %s", tc.path, got)
		}
	}
	if consoleLink("https://console.test", []byte(`{}`)) != "https://console.test/" {
		t.Fatal("old payload fallback")
	}
}
