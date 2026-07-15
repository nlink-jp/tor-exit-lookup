package torproject

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchOK(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		io.WriteString(w, "1.2.3.4\n5.6.7.8\n")
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "1.2.3.4\n5.6.7.8\n" {
		t.Errorf("body = %q", data)
	}
	if gotUA != userAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, userAgent)
	}
}

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("Fetch should error on HTTP 503")
	}
}
