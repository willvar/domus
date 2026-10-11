package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRejectFileContentPayloadAllowsHistoryRestoreFields(t *testing.T) {
	for _, route := range []string{
		"/file/history/restore",
		"/trash/example/history/restore",
	} {
		app := fiber.New()
		app.Post("/*", rejectFileContentPayload, func(c *fiber.Ctx) error {
			return c.SendStatus(http.StatusNoContent)
		})

		request := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{"path":"/file.txt","generation":1,"expected_generation":2}`))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("route %s status=%d, want %d", route, response.StatusCode, http.StatusNoContent)
		}
	}
}
