package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestCredentialSigningKeyRevokesOldToken(t *testing.T) {
	key := []byte("synthetic-signing-key-for-regression")
	oldKey := credentialSigningKey(key, "synthetic-old-password-hash")
	newKey := credentialSigningKey(key, "synthetic-new-password-hash")
	token, err := signJWT(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyJWT(token, oldKey) {
		t.Fatal("original token rejected")
	}
	if verifyJWT(token, newKey) {
		t.Fatal("old token accepted after credential change")
	}
	if verifyJWT(token, key) {
		t.Fatal("credential-bound token accepted under legacy key")
	}
}

func TestAuthenticationBodyLimit(t *testing.T) {
	s := &Server{}
	h := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		if err := decode(r, &body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	for _, route := range []string{"/api/auth/login", "/api/auth/init", "/api/auth/change-password"} {
		for _, chunked := range []bool{false, true} {
			r := httptest.NewRequest("POST", route, strings.NewReader(`{"password":"`+strings.Repeat("x", authBodyLimit)+`"}`))
			if chunked {
				r.ContentLength = -1
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
				t.Fatalf("route=%s chunked=%v status=%d: oversized body accepted", route, chunked, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"ordinary-password"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("ordinary login body rejected: %d", w.Code)
	}
}

func TestPasswordChangeAndResetRevokeSessions(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatalf("isolated PostgreSQL required: %v", err)
	}
	defer m.Close()
	if _, err := m.pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: m, jwtKey: []byte("synthetic-admin-session-signing-key")}
	call := func(handler http.HandlerFunc, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.requireAuth(handler).ServeHTTP(w, r)
		return w
	}
	getToken := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var body struct {
			Token string `json:"token"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Token == "" {
			t.Fatalf("token issuance failed: status=%d", w.Code)
		}
		return body.Token
	}
	accepted := func(server *Server, token string) int {
		r := httptest.NewRequest("GET", "/api/private-regression", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		return w.Code
	}
	old := getToken(call(s.authInit, "/api/auth/init", `{"password":"old-test-password"}`, ""))
	if accepted(s, old) != 204 {
		t.Fatal("initialized session rejected")
	}
	w := call(s.authChangePassword, "/api/auth/change-password", `{"old_password":"old-test-password","new_password":"new-test-password"}`, old)
	if w.Code != 200 {
		t.Fatalf("password change failed: %d", w.Code)
	}
	if accepted(s, old) != 401 {
		t.Fatal("old session remains valid after password change")
	}
	w = call(s.authChangePassword, "/api/auth/change-password", `{"old_password":"new-test-password","new_password":"unexpected-password"}`, old)
	if w.Code != 401 {
		t.Fatal("revoked token accepted at exempt change-password route")
	}
	current := getToken(call(s.authLogin, "/api/auth/login", `{"username":"ARTEX","password":"new-test-password"}`, ""))
	restarted := &Server{m: m, jwtKey: s.jwtKey}
	if accepted(restarted, old) != 401 || accepted(restarted, current) != 204 {
		t.Fatal("restart does not preserve revocation and current session")
	}
	// Simulate the existing reset-password script, which changes the persisted hash.
	hash, err := bcrypt.GenerateFromPassword([]byte("reset-test-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetSetting(authPassKey, string(hash)); err != nil {
		t.Fatal(err)
	}
	if accepted(s, current) != 401 {
		t.Fatal("session remains valid after administrative reset")
	}
	reset := getToken(call(s.authLogin, "/api/auth/login", `{"username":"ARTEX","password":"reset-test-password"}`, ""))
	if accepted(s, reset) != 204 {
		t.Fatal("new login after reset rejected")
	}
	if _, err := m.pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}
	if accepted(s, reset) != 503 {
		t.Fatal("database failure did not fail closed")
	}
}
