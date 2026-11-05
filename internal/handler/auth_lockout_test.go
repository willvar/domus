package handler

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/model"
)

func TestLoginLockoutStopsBeforeCredentialVerification(t *testing.T) {
	challenges := auth.NewChallengeManager()
	lookups := 0
	users := &model.MockUserRepo{
		GetByUsernameFn: func(string) (*model.User, error) { lookups++; return nil, errors.New("must not run") },
		GetByIDFn:       func(string) (*model.User, error) { lookups++; return nil, errors.New("must not run") },
	}
	repos := model.MockRepos()
	repos.Users = users
	h := &Handler{Challenges: challenges, Repos: repos, Audit: model.NewAuditWorker(nil)}
	token, err := challenges.CreateLoginChallenge(&auth.LoginChallenge{UserID: "alice", Username: "alice", Methods: []string{"email"}, EmailCode: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"password", "otp", "email"} {
		for i := 0; i < auth.MaxLoginAttempts; i++ {
			challenges.RecordLoginFailure("192.0.2.1", method, "alice")
		}
	}
	for _, test := range []struct {
		name, body string
		handler    httpx.Handler
	}{
		{"password", `{"username":"alice","password":"correct-password"}`, h.handleVerify},
		{"otp", `{"username":"alice","otp":"123456"}`, h.handleVerify},
		{"2fa", `{"token":"` + token + `","code":"123456","method":"email"}`, h.handleLogin},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/auth", strings.NewReader(test.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			lookups = 0
			test.handler.ServeHTTP(w, r)
			if lookups != 0 {
				t.Fatal("locked request continued into credential verification")
			}
			if w.Code != 429 || strings.Count(w.Body.String(), `"error"`) != 1 || !strings.Contains(w.Body.String(), `"retry_after":300`) {
				t.Fatalf("lockout response %d: %s", w.Code, w.Body)
			}
		})
	}
}
