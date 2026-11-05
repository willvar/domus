package oauth

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"domus/internal/httpx"
	"domus/internal/middleware"
)

func secureURL(uri *url.URL) bool {
	if uri == nil || uri.Host == "" || uri.User != nil || uri.Fragment != "" || uri.Opaque != "" {
		return false
	}
	if uri.Scheme == "https" {
		return true
	}
	host := uri.Hostname()
	ip := net.ParseIP(host)
	return uri.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback()))
}

func clientJSON(client Client) map[string]any {
	var redirects []string
	_ = json.Unmarshal([]byte(client.RedirectURIs), &redirects)
	return map[string]any{"client_id": client.ID, "name": client.Name, "public": client.Public,
		"redirect_uris": redirects, "created_at": client.CreatedAt, "disabled_at": client.DisabledAt}
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) error {
	var clients []Client
	if err := s.Store.database(r.Context()).Where("disabled_at IS NULL").Order("created_at DESC").Find(&clients).Error; err != nil {
		return err
	}
	result := make([]map[string]any, 0, len(clients))
	for _, client := range clients {
		result = append(result, clientJSON(client))
	}
	return httpx.JSON(w, 200, result)
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name         string   `json:"name"`
		Public       bool     `json:"public"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.NewError(400, "invalid_request")
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 100 || len(body.RedirectURIs) == 0 || len(body.RedirectURIs) > 10 {
		return httpx.NewError(400, "invalid_client")
	}
	for _, redirect := range body.RedirectURIs {
		uri, err := url.Parse(redirect)
		if err != nil || len(redirect) > 2048 || strings.TrimSpace(redirect) != redirect || !secureURL(uri) {
			return httpx.NewError(400, "invalid_redirect_uri")
		}
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	redirects, err := json.Marshal(body.RedirectURIs)
	if err != nil {
		return err
	}
	client := Client{ID: id, Name: body.Name, RedirectURIs: string(redirects), Public: body.Public}
	secret := ""
	if !body.Public {
		secret, err = randomID()
		if err != nil {
			return err
		}
		client.SecretHash, err = bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
	}
	if err := s.Store.database(r.Context()).Create(&client).Error; err != nil {
		return err
	}
	result := clientJSON(client)
	if secret != "" {
		result["client_secret"] = secret
	}
	return httpx.JSON(w, http.StatusCreated, result)
}

func (s *Server) disableClient(w http.ResponseWriter, r *http.Request) error {
	result := s.Store.database(r.Context()).Model(&Client{}).Where("id = ? AND disabled_at IS NULL", r.PathValue("id")).Update("disabled_at", time.Now().UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return httpx.NewError(404, "client_not_found")
	}
	return httpx.JSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) listGrants(w http.ResponseWriter, r *http.Request) error {
	type row struct {
		ID         string    `json:"id"`
		ClientID   string    `json:"client_id"`
		ClientName string    `json:"client_name"`
		CreatedAt  time.Time `json:"created_at"`
	}
	result := []row{}
	err := s.Store.database(r.Context()).Table("oauth_grants AS g").
		Select("g.id, g.client_id, c.name AS client_name, g.created_at").
		Joins("JOIN oauth_clients AS c ON c.id = g.client_id").
		Where("g.user_id = ? AND g.revoked_at IS NULL AND c.disabled_at IS NULL", middleware.Session(r).UserID).
		Order("g.created_at DESC").Scan(&result).Error
	if err != nil {
		return err
	}
	return httpx.JSON(w, 200, result)
}

func (s *Server) revokeGrant(w http.ResponseWriter, r *http.Request) error {
	result := s.Store.database(r.Context()).Model(&Grant{}).Where("id = ? AND user_id = ? AND revoked_at IS NULL", r.PathValue("id"), middleware.Session(r).UserID).Update("revoked_at", time.Now().UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return httpx.NewError(404, "grant_not_found")
	}
	return httpx.JSON(w, 200, map[string]bool{"ok": true})
}
