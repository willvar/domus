package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"domus/internal/middleware"
	"domus/internal/model"
)

func TestMoveReportsDOFSRenameFailure(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	repositories.Files = &model.MockFileRepo{
		GetFn: func(userID, path string) (*model.FileRecord, error) {
			return &model.FileRecord{ID: 42, UserID: userID, Path: path, Name: "source.txt", Status: "ready"}, nil
		},
		MoveFn: func(_, _, _, _ string) error {
			return errors.New("injected DOFS rename failure")
		},
	}

	request := httptest.NewRequest(http.MethodPost, "/file/move", strings.NewReader(
		`{"src_path":"/source.txt","dst_path":"/destination.txt","is_dir":false}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("move status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
}
