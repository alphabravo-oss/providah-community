package egress

import (
	"net/http/httptest"
	"testing"
)

func TestDestinationBoundary(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "169.254.169.254", "*.example.com", "example.com.", "example.com:443", "EXAMPLE.com", "-a.example.com", "a..example.com"} {
		if ValidHost(h) {
			t.Fatal("unsafe host", h)
		}
	}
	if !ValidHost("registry.opentofu.org") {
		t.Fatal("valid host rejected")
	}
	p := &Proxy{Hosts: []string{"registry.opentofu.org"}, Slots: make(chan struct{}, 1)}
	for _, target := range []string{"127.0.0.1:443", "registry.opentofu.org:80", "other.example.com:443"} {
		r := httptest.NewRequest("CONNECT", "http://"+target, nil)
		r.RequestURI = target
		r.Host = target
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("destination allowed", target, w.Code)
		}
	}
}
