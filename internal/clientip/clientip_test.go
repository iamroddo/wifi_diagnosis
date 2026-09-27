package clientip

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustParse(s string) net.IP { return net.ParseIP(s) }

func TestFromRequest_NoProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.50:12345"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	got := FromRequest(r, nil)
	if got != "192.168.1.50" {
		t.Errorf("got %q, want 192.168.1.50", got)
	}
}

func TestFromRequest_TrustedProxy_XFF(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:12345"
	r.Header.Set("X-Forwarded-For", "192.168.20.47, 10.0.0.2")

	trusted := []net.IP{mustParse("10.0.0.2")}
	got := FromRequest(r, trusted)
	if got != "192.168.20.47" {
		t.Errorf("got %q, want 192.168.20.47", got)
	}
}

func TestFromRequest_UntrustedProxy_XFF_Ignored(t *testing.T) {
	// The direct connection is NOT from a trusted proxy; ignore XFF.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:9999"
	r.Header.Set("X-Forwarded-For", "192.168.20.47")

	trusted := []net.IP{mustParse("10.0.0.2")}
	got := FromRequest(r, trusted)
	if got != "1.2.3.4" {
		t.Errorf("got %q, want 1.2.3.4 (untrusted proxy; XFF ignored)", got)
	}
}

func TestFromRequest_NoTrustedProxies_XFF_Ignored(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "192.168.1.5:8080",
		Header:     http.Header{"X-Forwarded-For": []string{"10.0.0.99"}},
	}
	got := FromRequest(r, nil)
	if got != "192.168.1.5" {
		t.Errorf("got %q, want 192.168.1.5", got)
	}
}
