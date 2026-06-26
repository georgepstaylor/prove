package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v78/github"
)

func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func TestNewRejectsInvalidKey(t *testing.T) {
	if _, err := New(123, []byte("not a pem")); err == nil {
		t.Fatal("expected error for invalid private key")
	}
}

func TestNewAcceptsValidKey(t *testing.T) {
	if _, err := New(123, testKeyPEM(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// repoClientFor builds a RepoClient whose REST calls hit the given test server.
func repoClientFor(t *testing.T, h http.Handler) *RepoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	gh := github.NewClient(nil)
	u, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = u
	return &RepoClient{gh: gh}
}

func TestGetFileFound(t *testing.T) {
	body := "allow_dot_github: true\n"
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	c := repoClientFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q,"path":".github/prove.yml"}`, enc)
	}))

	data, found, err := c.GetFile(context.Background(), "o", "r", ".github/prove.yml", "main")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if string(data) != body {
		t.Fatalf("content: got %q, want %q", data, body)
	}
}

func TestGetFileNotFound(t *testing.T) {
	c := repoClientFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, found, err := c.GetFile(context.Background(), "o", "r", ".github/prove.yml", "main")
	if err != nil {
		t.Fatalf("GetFile should not error on 404: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestListChangedFiles(t *testing.T) {
	c := repoClientFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[
			{"filename":"playground/alice/x.md","status":"added"},
			{"filename":"docs/new.md","previous_filename":"docs/old.md","status":"renamed"}
		]`)
	}))

	got, err := c.ListChangedFiles(context.Background(), "o", "r", 7, 3000)
	if err != nil {
		t.Fatalf("ListChangedFiles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("count: got %d, want 2", len(got))
	}
	if got[0].Path != "playground/alice/x.md" {
		t.Errorf("file 0 path: got %q", got[0].Path)
	}
	if got[1].Previous != "docs/old.md" {
		t.Errorf("rename previous: got %q, want docs/old.md", got[1].Previous)
	}
}
