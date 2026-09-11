package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameTec-live/geocoder-go/geocoder"
	"github.com/GameTec-live/geocoder-go/internal/api"
	"github.com/GameTec-live/geocoder-go/internal/pack"
	"github.com/gin-gonic/gin"
)

func TestEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	builder, err := pack.Create(context.Background(), filepath.Join(root, "sample.sqlite"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(context.Background(), pack.Record{Kind: "address", Street: "Stephansplatz", HouseNumber: "1", Locality: "Wien", CountryCode: "AT", Latitude: 48.20849, Longitude: 16.37208}); err != nil {
		t.Fatal(err)
	}
	if err := builder.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := builder.Close(); err != nil {
		t.Fatal(err)
	}
	service, err := geocoder.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	router := api.Router(service)

	for _, test := range []struct {
		url        string
		wantStatus int
		wantCount  int
	}{
		{"/healthz", http.StatusOK, 0},
		{"/readyz", http.StatusOK, 0},
		{"/geocode?q=Stephansplatz+1", http.StatusOK, 1},
		{"/reverse?lat=48.2085&lon=16.3721&radius_m=100", http.StatusOK, 1},
		{"/geocode", http.StatusBadRequest, 0},
		{"/reverse?lat=no&lon=16", http.StatusBadRequest, 0},
		{"/", http.StatusNotFound, 0},
	} {
		request := httptest.NewRequest(http.MethodGet, test.url, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Fatalf("%s returned %d: %s", test.url, response.Code, response.Body.String())
		}
		if test.wantStatus == http.StatusOK && (strings.HasPrefix(test.url, "/geocode") || strings.HasPrefix(test.url, "/reverse")) {
			var body struct {
				Count int `json:"count"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Count != test.wantCount {
				t.Fatalf("%s returned count %d, want %d", test.url, body.Count, test.wantCount)
			}
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s is missing production response headers: %#v", test.url, response.Header())
		}
	}

	tooLong := httptest.NewRecorder()
	router.ServeHTTP(tooLong, httptest.NewRequest(http.MethodGet, "/geocode?q="+strings.Repeat("a", 513), nil))
	if tooLong.Code != http.StatusBadRequest || !strings.Contains(tooLong.Body.String(), "query_too_long") {
		t.Fatalf("long query was not rejected: %d %s", tooLong.Code, tooLong.Body.String())
	}

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var readiness struct {
		Status    string `json:"status"`
		PackCount int    `json:"pack_count"`
	}
	if err := json.Unmarshal(ready.Body.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.Status != "ready" || readiness.PackCount != 1 {
		t.Fatalf("unexpected readiness response: %#v", readiness)
	}
}
