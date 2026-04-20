package resources

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPServerFileAcceptHTTPHost(t *testing.T) {
	testCases := []struct {
		name         string
		resourceHost string
		requestHost  string
		requestPath  string
		wantErr      bool
	}{
		{
			name:         "path-only file ignores host",
			resourceHost: "",
			requestHost:  "site.example.test",
			requestPath:  "/.well-known/acme-challenge/token",
		},
		{
			name:         "host match exact",
			resourceHost: "site.example.test",
			requestHost:  "site.example.test",
			requestPath:  "/.well-known/acme-challenge/token",
		},
		{
			name:         "host match strips port and ignores case",
			resourceHost: "Site.Example.Test",
			requestHost:  "site.example.test:80",
			requestPath:  "/.well-known/acme-challenge/token",
		},
		{
			name:         "host mismatch rejected",
			resourceHost: "api.example.test",
			requestHost:  "site.example.test",
			requestPath:  "/.well-known/acme-challenge/token",
			wantErr:      true,
		},
		{
			name:         "path mismatch rejected",
			resourceHost: "site.example.test",
			requestHost:  "site.example.test",
			requestPath:  "/healthz",
			wantErr:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res := &HTTPServerFileRes{
				Filename: "/.well-known/acme-challenge/token",
				Host:     tc.resourceHost,
				Data:     "key-authorization",
			}
			req := httptest.NewRequest(http.MethodGet, "http://example.test"+tc.requestPath, nil)
			req.Host = tc.requestHost

			err := res.AcceptHTTP(req)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got: %v", err)
			}
		})
	}
}
