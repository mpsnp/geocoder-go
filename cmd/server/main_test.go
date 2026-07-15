package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := healthcheck([]string{server.URL}); err != nil {
		t.Fatalf("healthy endpoint failed: %v", err)
	}
}

func TestHealthcheckRejectsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := healthcheck([]string{server.URL}); err == nil {
		t.Fatal("unhealthy endpoint unexpectedly passed")
	}
}
