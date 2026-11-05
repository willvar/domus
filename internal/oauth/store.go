// Package oauth provides the persistent, file-read-only OAuth authorization server.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/handler/pkce"
	"github.com/ory/fosite/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"domus/internal/model"
)

const ReadScope = "files:read"

type Client struct {
	ID           string     `gorm:"primaryKey;size:64" json:"client_id"`
	Name         string     `gorm:"not null" json:"name"`
	RedirectURIs string     `gorm:"not null" json:"-"`
	SecretHash   []byte     `json:"-"`
	Public       bool       `gorm:"not null" json:"public"`
	CreatedAt    time.Time  `json:"created_at"`
	DisabledAt   *time.Time `json:"disabled_at"`
}

func (Client) TableName() string { return "oauth_clients" }

type Grant struct {
	ID        string     `gorm:"primaryKey;size:64" json:"id"`
	ClientID  string     `gorm:"not null;index" json:"client_id"`
	UserID    string     `gorm:"not null;index" json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

func (Grant) TableName() string { return "oauth_grants" }

// Only token signatures and a whitelisted request snapshot are persisted, never
// bearer tokens, client secrets, file keys or a browser login session.
type credential struct {
	Kind      string    `gorm:"primaryKey;size:16"`
	Signature string    `gorm:"primaryKey;size:128"`
	GrantID   string    `gorm:"not null;index"`
	Payload   string    `gorm:"not null;type:text"`
	Active    bool      `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null;index"`
}

func (credential) TableName() string { return "oauth_credentials" }

type consent struct {
	ID          string    `gorm:"primaryKey;size:64"`
	UserID      string    `gorm:"not null;index"`
	SessionHash string    `gorm:"not null"`
	Query       string    `gorm:"not null;type:text"`
	ExpiresAt   time.Time `gorm:"not null;index"`
}

func (consent) TableName() string { return "oauth_consents" }

type Store struct{ db *gorm.DB }

var (
	_ oauth2.CoreStorage            = (*Store)(nil)
	_ oauth2.TokenRevocationStorage = (*Store)(nil)
	_ pkce.PKCERequestStorage       = (*Store)(nil)
	_ storage.Transactional         = (*Store)(nil)
)

func OpenStore(db *gorm.DB) (*Store, error) {
	if err := db.AutoMigrate(&Client{}, &Grant{}, &credential{}, &consent{}); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

type txKey struct{}
type txOwnerKey struct{}
type transaction struct {
	db      *gorm.DB
	revoked bool
}

func (s *Store) database(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*transaction); ok {
		return tx.db.WithContext(ctx)
	}
	return s.db.WithContext(ctx)
}

func (s *Store) BeginTX(ctx context.Context) (context.Context, error) {
	if _, ok := ctx.Value(txKey{}).(*transaction); ok {
		return context.WithValue(ctx, txOwnerKey{}, false), nil
	}
	db := s.db.WithContext(ctx).Begin()
	if db.Error != nil {
		return ctx, db.Error
	}
	ctx = context.WithValue(ctx, txKey{}, &transaction{db: db})
	return context.WithValue(ctx, txOwnerKey{}, true), nil
}

func (s *Store) Commit(ctx context.Context) error {
	if owner, _ := ctx.Value(txOwnerKey{}).(bool); !owner {
		return nil
	}
	return s.database(ctx).Commit().Error
}

func (s *Store) Rollback(ctx context.Context) error {
	if owner, _ := ctx.Value(txOwnerKey{}).(bool); !owner {
		return nil
	}
	err := s.database(ctx).Rollback().Error
	if errors.Is(err, gorm.ErrInvalidTransaction) {
		return nil
	}
	return err
}

// Fosite's PKCE validation happens before its own token-issuance transaction.
// Wrap the whole endpoint so failed verification cannot consume PKCE, and two
// exchanges/refreshes cannot both validate the same live credential.
func (s *Store) tokenTransaction(ctx context.Context, fn func(context.Context) error) error {
	return s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		tx := &transaction{db: db}
		ctx = context.WithValue(ctx, txKey{}, tx)
		err := fn(ctx)
		// Replay is an OAuth error, but its family revocation must be committed.
		if tx.revoked {
			return nil
		}
		return err
	})
}

func dbError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fosite.ErrNotFound
	}
	return err
}

func (s *Store) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	var client Client
	query := s.database(ctx)
	if ctx.Value(txKey{}) != nil {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := query.Where("id = ? AND disabled_at IS NULL", id).First(&client).Error; err != nil {
		return nil, dbError(err)
	}
	var redirects []string
	if err := json.Unmarshal([]byte(client.RedirectURIs), &redirects); err != nil {
		return nil, err
	}
	return &fosite.DefaultClient{
		ID: client.ID, Secret: client.SecretHash, RedirectURIs: redirects, Public: client.Public,
		GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
		Scopes: []string{ReadScope},
	}, nil
}

// JWT client assertions are not an enabled authentication method.
func (*Store) ClientAssertionJWTValid(context.Context, string) error {
	return fosite.ErrRequestUnauthorized
}
func (*Store) SetClientAssertionJWT(context.Context, string, time.Time) error {
	return fosite.ErrRequestUnauthorized
}

type requestSnapshot struct {
	ID             string
	ClientID       string
	RequestedAt    time.Time
	RequestedScope fosite.Arguments
	GrantedScope   fosite.Arguments
	Form           url.Values
	Session        fosite.DefaultSession
}

func (s *Store) create(ctx context.Context, kind, signature string, requester fosite.Requester) error {
	session, ok := requester.GetSession().(*fosite.DefaultSession)
	if !ok {
		return errors.New("unexpected OAuth session type")
	}
	grantID, _ := session.Extra["grant_id"].(string)
	if grantID == "" || grantID != requester.GetID() {
		return errors.New("OAuth grant is required")
	}
	if _, err := s.liveGrant(ctx, grantID, requester.GetClient().GetID(), session.Subject); err != nil {
		return err
	}
	form := url.Values{}
	for _, name := range []string{"redirect_uri", "code_challenge", "code_challenge_method"} {
		if values := requester.GetRequestForm()[name]; len(values) > 0 {
			form[name] = values
		}
	}
	clean := fosite.DefaultSession{Subject: session.Subject, ExpiresAt: session.ExpiresAt, Extra: map[string]any{"grant_id": grantID}}
	payload, err := json.Marshal(requestSnapshot{
		ID: requester.GetID(), ClientID: requester.GetClient().GetID(), RequestedAt: requester.GetRequestedAt(),
		RequestedScope: requester.GetRequestedScopes(), GrantedScope: requester.GetGrantedScopes(), Form: form, Session: clean,
	})
	if err != nil {
		return err
	}
	expires := session.GetExpiresAt(fosite.AccessToken)
	switch kind {
	case "code", "pkce":
		expires = session.GetExpiresAt(fosite.AuthorizeCode)
	case "refresh":
		expires = session.GetExpiresAt(fosite.RefreshToken)
	}
	if expires.IsZero() {
		return errors.New("OAuth credential expiry is required")
	}
	return s.database(ctx).Create(&credential{Kind: kind, Signature: signature, GrantID: grantID, Payload: string(payload), Active: true, ExpiresAt: expires}).Error
}

func (s *Store) liveGrant(ctx context.Context, id, clientID, userID string) (*Grant, error) {
	// Lock order is client -> grant -> credential everywhere that issues tokens.
	if _, err := s.GetClient(ctx, clientID); err != nil {
		return nil, err
	}
	query := s.database(ctx)
	if ctx.Value(txKey{}) != nil {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var grant Grant
	if err := query.Where("id = ? AND client_id = ? AND user_id = ? AND revoked_at IS NULL", id, clientID, userID).First(&grant).Error; err != nil {
		return nil, dbError(err)
	}
	var user model.User
	if err := s.database(ctx).Select("id").Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, dbError(err)
	}
	return &grant, nil
}

func (s *Store) get(ctx context.Context, kind, signature string) (fosite.Requester, error) {
	var row credential
	if err := s.database(ctx).Where("kind = ? AND signature = ?", kind, signature).First(&row).Error; err != nil {
		return nil, dbError(err)
	}
	var snapshot requestSnapshot
	if err := json.Unmarshal([]byte(row.Payload), &snapshot); err != nil {
		return nil, err
	}
	if _, err := s.liveGrant(ctx, row.GrantID, snapshot.ClientID, snapshot.Session.Subject); err != nil {
		return nil, err
	}
	if ctx.Value(txKey{}) != nil {
		if err := s.database(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind = ? AND signature = ?", kind, signature).First(&row).Error; err != nil {
			return nil, dbError(err)
		}
	}
	client, err := s.GetClient(ctx, snapshot.ClientID)
	if err != nil {
		return nil, err
	}
	request := &fosite.Request{ID: snapshot.ID, Client: client, RequestedAt: snapshot.RequestedAt,
		RequestedScope: snapshot.RequestedScope, GrantedScope: snapshot.GrantedScope, Form: snapshot.Form, Session: &snapshot.Session}
	if !row.Active {
		switch kind {
		case "code":
			return request, fosite.ErrInvalidatedAuthorizeCode
		case "refresh":
			return request, fosite.ErrInactiveToken
		default:
			return nil, fosite.ErrNotFound
		}
	}
	if !time.Now().Before(row.ExpiresAt) {
		return nil, fosite.ErrNotFound
	}
	return request, nil
}

func (s *Store) invalidate(ctx context.Context, kind, signature string) error {
	result := s.database(ctx).Model(&credential{}).Where("kind = ? AND signature = ? AND active = ?", kind, signature, true).Update("active", false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fosite.ErrInactiveToken
	}
	return nil
}

func (s *Store) CreateAuthorizeCodeSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.create(ctx, "code", signature, req)
}
func (s *Store) GetAuthorizeCodeSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return s.get(ctx, "code", signature)
}
func (s *Store) InvalidateAuthorizeCodeSession(ctx context.Context, signature string) error {
	return s.invalidate(ctx, "code", signature)
}
func (s *Store) CreateAccessTokenSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.create(ctx, "access", signature, req)
}
func (s *Store) GetAccessTokenSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return s.get(ctx, "access", signature)
}
func (s *Store) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	return s.invalidate(ctx, "access", signature)
}
func (s *Store) CreateRefreshTokenSession(ctx context.Context, signature, _ string, req fosite.Requester) error {
	return s.create(ctx, "refresh", signature, req)
}
func (s *Store) GetRefreshTokenSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return s.get(ctx, "refresh", signature)
}
func (s *Store) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	// Keep consumed records: subsequent reuse must still identify the family.
	return s.database(ctx).Model(&credential{}).Where("kind = ? AND signature = ?", "refresh", signature).Update("active", false).Error
}

func (s *Store) RotateRefreshToken(ctx context.Context, requestID, signature string) error {
	if err := s.invalidate(ctx, "refresh", signature); err != nil {
		return err
	}
	return s.database(ctx).Model(&credential{}).Where("grant_id = ? AND kind = ?", requestID, "access").Update("active", false).Error
}

func (s *Store) revoke(ctx context.Context, requestID string) error {
	err := s.database(ctx).Model(&Grant{}).Where("id = ? AND revoked_at IS NULL", requestID).Update("revoked_at", time.Now().UTC()).Error
	if err == nil {
		if tx, ok := ctx.Value(txKey{}).(*transaction); ok {
			tx.revoked = true
		}
	}
	return err
}

func (s *Store) RevokeAccessToken(ctx context.Context, requestID string) error {
	return s.revoke(ctx, requestID)
}
func (s *Store) RevokeRefreshToken(ctx context.Context, requestID string) error {
	return s.revoke(ctx, requestID)
}
func (s *Store) CreatePKCERequestSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.create(ctx, "pkce", signature, req)
}
func (s *Store) GetPKCERequestSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return s.get(ctx, "pkce", signature)
}
func (s *Store) DeletePKCERequestSession(ctx context.Context, signature string) error {
	return s.invalidate(ctx, "pkce", signature)
}

func (s *Store) CleanExpired(ctx context.Context) error {
	// Consumed code/refresh records outlive their token expiry while a grant is
	// live, otherwise a later replay could no longer revoke that token family.
	return s.database(ctx).Transaction(func(db *gorm.DB) error {
		if err := db.Where("expires_at < ?", time.Now()).Delete(&consent{}).Error; err != nil {
			return err
		}
		if err := db.Where("kind IN ? AND expires_at < ?", []string{"access", "pkce"}, time.Now().Add(-24*time.Hour)).Delete(&credential{}).Error; err != nil {
			return err
		}
		revoked := db.Model(&Grant{}).Select("id").Where("revoked_at < ?", time.Now().Add(-31*24*time.Hour))
		return db.Where("grant_id IN (?)", revoked).Delete(&credential{}).Error
	})
}
