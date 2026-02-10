package cert

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
)

func TestGetCSRList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []CSRResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, CSRResponse{
					Id:         fmt.Sprintf("csr-%d", i),
					CommonName: fmt.Sprintf("cn-%d", i),
					Authority: AuthorityResponse{
						Name: "auth-test",
					},
					RequestedBy: iam.UserSummary{Name: "admin"},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, CSRResponse{
					Id:         fmt.Sprintf("csr-p2-%d", i),
					CommonName: fmt.Sprintf("cn-p2-%d", i),
					Authority: AuthorityResponse{
						Name: "auth-test",
					},
					RequestedBy: iam.UserSummary{Name: "admin"},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[CSRResponse]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	csrs, err := GetCSRList(ac, "pending")
	if err != nil {
		t.Fatalf("GetCSRList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(csrs) != 150 {
		t.Errorf("expected 150 CSRs, got %d", len(csrs))
	}
}

func TestGetAuthorityList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []AuthorityResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, AuthorityResponse{
					Id:   fmt.Sprintf("auth-%d", i),
					Name: fmt.Sprintf("authority-%d", i),
					Owner: iam.UserSummary{
						Name: "admin",
					},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, AuthorityResponse{
					Id:   fmt.Sprintf("auth-p2-%d", i),
					Name: fmt.Sprintf("authority-p2-%d", i),
					Owner: iam.UserSummary{
						Name: "admin",
					},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[AuthorityResponse]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	authorities, err := GetAuthorityList(ac)
	if err != nil {
		t.Fatalf("GetAuthorityList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(authorities) != 150 {
		t.Errorf("expected 150 authorities, got %d", len(authorities))
	}
}

func TestGetCertificateList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []Certificate
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, Certificate{
					Id: fmt.Sprintf("cert-%d", i),
					Authority: AuthoritySummary{
						Name: "auth-test",
					},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, Certificate{
					Id: fmt.Sprintf("cert-p2-%d", i),
					Authority: AuthoritySummary{
						Name: "auth-test",
					},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[Certificate]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	certs, err := GetCertificateList(ac)
	if err != nil {
		t.Fatalf("GetCertificateList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(certs) != 150 {
		t.Errorf("expected 150 certificates, got %d", len(certs))
	}
}
