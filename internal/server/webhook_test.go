package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v78/github"
)

const testSecret = "topsecret"

// signedRequest builds a webhook POST with a valid X-Hub-Signature-256 unless
// overrideSig is non-empty (used to simulate a bad/missing signature).
func signedRequest(eventType, body, overrideSig string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-GitHub-Delivery", "test-delivery-1")

	sig := overrideSig
	if sig == "" {
		mac := hmac.New(sha256.New, []byte(testSecret))
		mac.Write([]byte(body))
		sig = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	if sig != "none" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	return req
}

func postWebhook(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestWebhookValidSignatureDispatches(t *testing.T) {
	rp := newRecordingProcessor()
	s := newTestServer(Config{WebhookSecret: testSecret}, rp)

	body := `{"action":"opened","number":7,"pull_request":{"user":{"login":"alice"}}}`
	rec := postWebhook(s, signedRequest("pull_request", body, ""))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusAccepted)
	}

	select {
	case <-rp.done:
	case <-time.After(2 * time.Second):
		t.Fatal("processor was not invoked")
	}

	if len(rp.events) != 1 {
		t.Fatalf("events recorded: got %d, want 1", len(rp.events))
	}
	pr, ok := rp.events[0].(*github.PullRequestEvent)
	if !ok {
		t.Fatalf("event type: got %T, want *github.PullRequestEvent", rp.events[0])
	}
	if pr.GetPullRequest().GetUser().GetLogin() != "alice" {
		t.Fatalf("author: got %q, want alice", pr.GetPullRequest().GetUser().GetLogin())
	}
}

func TestWebhookInvalidSignatureRejected(t *testing.T) {
	rp := newRecordingProcessor()
	s := newTestServer(Config{WebhookSecret: testSecret}, rp)

	rec := postWebhook(s, signedRequest("pull_request", `{"action":"opened"}`, "sha256=deadbeef"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	select {
	case <-rp.done:
		t.Fatal("processor should not run on invalid signature")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWebhookMissingSignatureRejected(t *testing.T) {
	s := newTestServer(Config{WebhookSecret: testSecret}, newRecordingProcessor())
	rec := postWebhook(s, signedRequest("pull_request", `{"action":"opened"}`, "none"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookMalformedPayloadRejected(t *testing.T) {
	s := newTestServer(Config{WebhookSecret: testSecret}, newRecordingProcessor())
	rec := postWebhook(s, signedRequest("pull_request", `{not json`, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
