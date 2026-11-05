package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectFileContentPayloadAllowsHistoryRestoreFields(t *testing.T) {
	for _, route := range []string{
		"/file/history/restore",
		"/trash/example/history/restore",
	} {
		app := http.NewServeMux()
		app.Handle("POST /", rejectFileContentPayload(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))

		request := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{"path":"/file.txt","generation":1,"expected_generation":2}`))
		request.Header.Set("Content-Type", "application/json")
		response, err := testRequest(app, request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("route %s status=%d, want %d", route, response.StatusCode, http.StatusNoContent)
		}
	}
}
