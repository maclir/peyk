package owner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGrantSetsClaimOnTheAccountWithThatEmail(t *testing.T) {
	var updated map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/projects/p/accounts:lookup":
			if body["email"].([]any)[0] != "me@example.com" {
				w.Write([]byte(`{}`))
				return
			}
			w.Write([]byte(`{"users":[{"localId":"uid-1"}]}`))
		case "/v1/projects/p/accounts:update":
			updated = body
			w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	uid, err := Grant(context.Background(), srv.Client(), srv.URL, "p", "me@example.com")
	if err != nil || uid != "uid-1" {
		t.Fatalf("Grant = %q, %v", uid, err)
	}
	if updated["localId"] != "uid-1" || updated["customAttributes"] != `{"owner":true}` {
		t.Fatalf("update body = %v", updated)
	}

	_, err = Grant(context.Background(), srv.Client(), srv.URL, "p", "other@example.com")
	if err == nil || !strings.Contains(err.Error(), "sign in once") {
		t.Fatalf("unknown email: %v", err)
	}
}
