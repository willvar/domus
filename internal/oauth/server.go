package oauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"domus/internal/httpx"
	"domus/internal/middleware"
)

type Server struct {
	Store    *Store
	Provider fosite.OAuth2Provider
	origin   string
}

func NewServer(db *gorm.DB, origin, secret string) (*Server, error) {
	store, err := OpenStore(db)
	if err != nil {
		return nil, err
	}
	key := hmac.New(sha256.New, []byte(secret))
	_, _ = key.Write([]byte("domus/oauth-tokens/v1"))
	cfg := &fosite.Config{
		GlobalSecret: key.Sum(nil), AccessTokenLifespan: 15 * time.Minute,
		RefreshTokenLifespan: 30 * 24 * time.Hour, AuthorizeCodeLifespan: 5 * time.Minute,
		ScopeStrategy: fosite.ExactScopeStrategy, RefreshTokenScopes: []string{ReadScope},
		EnforcePKCE: true, EnablePKCEPlainChallengeMethod: false,
		DisableRefreshTokenValidation: true,
		RedirectSecureChecker:         func(_ context.Context, uri *url.URL) bool { return secureURL(uri) },
	}
	provider := compose.Compose(cfg, store, compose.NewOAuth2HMACStrategy(cfg),
		compose.OAuth2AuthorizeExplicitFactory, compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2PKCEFactory, compose.OAuth2TokenIntrospectionFactory, compose.OAuth2TokenRevocationFactory)
	return &Server{Store: store, Provider: provider, origin: strings.TrimSuffix(origin, "/")}, nil
}

func (s *Server) RegisterRoutes(mux *http.ServeMux, mid *middleware.Middleware) {
	public := func(pattern string, handler http.Handler) {
		mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			handler.ServeHTTP(w, r)
		}))
	}
	private := func(pattern string, handler httpx.Handler, root bool) {
		var endpoint http.Handler = handler
		if root {
			endpoint = middleware.RootRequired(endpoint)
		}
		endpoint = mid.AuthRequired(endpoint)
		public(pattern, httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			// A browser login cookie never authorizes a cross-origin OAuth change.
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != s.origin {
				return httpx.NewError(403, "origin_required")
			}
			endpoint.ServeHTTP(w, r)
			return nil
		}))
	}
	public("GET /.well-known/oauth-authorization-server", httpx.Handler(s.metadata))
	public("GET /oauth/authorize", httpx.Handler(s.authorize))
	public("POST /oauth/token", http.HandlerFunc(s.token))
	public("POST /oauth/revoke", http.HandlerFunc(s.revoke))
	private("POST /oauth/consent", s.consent, false)
	private("POST /oauth/decision", s.decision, false)
	private("GET /oauth/grants", s.listGrants, false)
	private("DELETE /oauth/grants/{id}", s.revokeGrant, false)
	private("GET /oauth/clients", s.listClients, true)
	private("POST /oauth/clients", s.createClient, true)
	private("DELETE /oauth/clients/{id}", s.disableClient, true)
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) error {
	return httpx.JSON(w, 200, map[string]any{
		"issuer": s.origin, "authorization_endpoint": s.origin + "/oauth/authorize",
		"token_endpoint": s.origin + "/oauth/token", "revocation_endpoint": s.origin + "/oauth/revoke",
		"scopes_supported": []string{ReadScope}, "response_types_supported": []string{"code"},
		"response_modes_supported": []string{"query"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"client_secret_basic", "client_secret_post", "none"},
		"revocation_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"webdav_endpoint":                            s.origin + "/dav/",
	})
}

func (s *Server) authorizeRequest(ctx context.Context, query string) (fosite.AuthorizeRequester, error) {
	if len(query) > 8192 {
		return nil, fosite.ErrInvalidRequest
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return nil, fosite.ErrInvalidRequest
	}
	for key, items := range values {
		if len(items) != 1 || !slices.Contains([]string{"client_id", "redirect_uri", "response_type", "scope", "state", "code_challenge", "code_challenge_method", "response_mode"}, key) {
			return nil, fosite.ErrInvalidRequest
		}
	}
	if mode := values.Get("response_mode"); mode != "" && mode != "query" {
		return nil, fosite.ErrInvalidRequest
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.origin+"/oauth/authorize?"+values.Encode(), nil)
	if err != nil {
		return nil, fosite.ErrInvalidRequest
	}
	ar, err := s.Provider.NewAuthorizeRequest(ctx, request)
	if err != nil {
		return ar, err
	}
	if values.Get("response_type") != "code" || (values.Get("response_mode") != "" && values.Get("response_mode") != "query") {
		return ar, fosite.ErrUnsupportedResponseType
	}
	if values.Get("scope") != ReadScope {
		return ar, fosite.ErrInvalidScope
	}
	if !slices.Contains(ar.GetClient().GetRedirectURIs(), values.Get("redirect_uri")) {
		return nil, fosite.ErrInvalidRequest
	}
	challenge, err := base64.RawURLEncoding.DecodeString(values.Get("code_challenge"))
	if values.Get("code_challenge_method") != "S256" || err != nil || len(challenge) != sha256.Size || len(values.Get("code_challenge")) != 43 {
		return ar, fosite.ErrInvalidRequest.WithHint("PKCE S256 is required.")
	}
	return ar, nil
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) error {
	ar, err := s.authorizeRequest(r.Context(), r.URL.RawQuery)
	if err != nil {
		if ar == nil {
			ar = fosite.NewAuthorizeRequest()
		}
		s.Provider.WriteAuthorizeError(r.Context(), w, ar, err)
		return nil
	}
	http.Redirect(w, r, s.origin+"/authorize?"+r.URL.RawQuery, http.StatusSeeOther)
	return nil
}

func randomID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func sessionHash(id string) string {
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:])
}

func (s *Server) consent(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Query string `json:"query"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.NewError(400, "invalid_request")
	}
	ar, err := s.authorizeRequest(r.Context(), body.Query)
	if err != nil {
		return httpx.NewError(400, "invalid_authorization_request")
	}
	var client Client
	if err := s.Store.database(r.Context()).Where("id = ? AND disabled_at IS NULL", ar.GetClient().GetID()).First(&client).Error; err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	session := middleware.Session(r)
	row := consent{ID: id, UserID: session.UserID, SessionHash: sessionHash(middleware.SessionID(r)), Query: body.Query, ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err := s.Store.database(r.Context()).Create(&row).Error; err != nil {
		return err
	}
	return httpx.JSON(w, 200, map[string]any{"challenge": id, "client_name": client.Name, "client_id": client.ID,
		"redirect_uri": ar.GetRedirectURI().String(), "scope": ReadScope, "expires_at": row.ExpiresAt})
}

func (s *Server) decision(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return httpx.NewError(400, "invalid_request")
	}
	challenge, decision := r.PostForm.Get("challenge"), r.PostForm.Get("decision")
	if challenge == "" || (decision != "allow" && decision != "deny") {
		return httpx.NewError(400, "invalid_request")
	}
	var ar fosite.AuthorizeRequester
	var response fosite.AuthorizeResponder
	var protocolErr error
	err := s.Store.db.WithContext(r.Context()).Transaction(func(db *gorm.DB) error {
		ctx := context.WithValue(r.Context(), txKey{}, &transaction{db: db})
		session := middleware.Session(r)
		var row consent
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND session_hash = ? AND expires_at > ?",
			challenge, session.UserID, sessionHash(middleware.SessionID(r)), time.Now()).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return httpx.NewError(400, "invalid_consent_challenge")
			}
			return err
		}
		if err := db.Delete(&row).Error; err != nil {
			return err
		}
		ar, protocolErr = s.authorizeRequest(ctx, row.Query)
		if protocolErr != nil {
			return nil
		}
		if decision == "deny" {
			protocolErr = fosite.ErrAccessDenied
			return nil
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		grant := Grant{ID: id, ClientID: ar.GetClient().GetID(), UserID: session.UserID}
		if err := db.Create(&grant).Error; err != nil {
			return err
		}
		ar.SetID(id)
		ar.GrantScope(ReadScope)
		oauthSession := &fosite.DefaultSession{Subject: session.UserID, Extra: map[string]any{"grant_id": id}}
		response, protocolErr = s.Provider.NewAuthorizeResponse(ctx, ar, oauthSession)
		return protocolErr
	})
	if err != nil {
		return err
	}
	if protocolErr != nil {
		if ar == nil {
			ar = fosite.NewAuthorizeRequest()
		}
		s.Provider.WriteAuthorizeError(r.Context(), w, ar, protocolErr)
	} else {
		s.Provider.WriteAuthorizeResponse(r.Context(), w, ar, response)
	}
	return nil
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := parseTokenForm(r); err != nil {
		s.Provider.WriteAccessError(r.Context(), w, nil, err)
		return
	}
	var request fosite.AccessRequester
	var response fosite.AccessResponder
	var protocolErr error
	err := s.Store.tokenTransaction(r.Context(), func(ctx context.Context) error {
		request, protocolErr = s.Provider.NewAccessRequest(ctx, r.WithContext(ctx), &fosite.DefaultSession{})
		if protocolErr == nil {
			response, protocolErr = s.Provider.NewAccessResponse(ctx, request)
		}
		return protocolErr
	})
	if err != nil && err != protocolErr {
		protocolErr = fosite.ErrServerError.WithWrap(err)
	}
	if protocolErr != nil {
		s.Provider.WriteAccessError(r.Context(), w, request, protocolErr)
		return
	}
	s.Provider.WriteAccessResponse(r.Context(), w, request, response)
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if err := parseTokenForm(r); err != nil {
		s.Provider.WriteRevocationResponse(r.Context(), w, err)
		return
	}
	err := s.Provider.NewRevocationRequest(r.Context(), r)
	if errors.Is(err, fosite.ErrTemporarilyUnavailable) || errors.Is(err, fosite.ErrServerError) {
		_ = httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily_unavailable"})
		return
	}
	s.Provider.WriteRevocationResponse(r.Context(), w, err)
}

func parseTokenForm(r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" {
		return fosite.ErrInvalidRequest
	}
	if err := r.ParseForm(); err != nil {
		return fosite.ErrInvalidRequest
	}
	for name, values := range r.PostForm {
		if len(values) != 1 {
			return fosite.ErrInvalidRequest
		}
		if !slices.Contains([]string{"grant_type", "code", "code_verifier", "redirect_uri", "client_id", "client_secret", "refresh_token", "scope", "token", "token_type_hint"}, name) {
			return fosite.ErrInvalidRequest
		}
	}
	if scope := r.PostForm.Get("scope"); scope != "" && scope != ReadScope {
		return fosite.ErrInvalidScope
	}
	return nil
}

func (s *Server) Authenticate(r *http.Request) (string, error) {
	// A cookie, query token or refresh token must never unlock the DAV endpoint.
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fosite.ErrRequestUnauthorized
	}
	use, request, err := s.Provider.IntrospectToken(r.Context(), parts[1], fosite.AccessToken, &fosite.DefaultSession{}, ReadScope)
	if err != nil || use != fosite.AccessToken {
		return "", fosite.ErrRequestUnauthorized
	}
	return request.GetSession().GetSubject(), nil
}
