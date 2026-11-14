package zitadel

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// zitadelAPIScope grants access to the Zitadel APIs.
const zitadelAPIScope = "openid urn:zitadel:iam:org:project:id:zitadel:aud"

// TokenSource returns a bearer token for the Zitadel API.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// staticToken is a personal access token.
type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

// ServiceAccountKey is the JSON key of a Zitadel service account (machine user).
type ServiceAccountKey struct {
	Type   string `json:"type"`
	KeyID  string `json:"keyId"`
	Key    string `json:"key"`
	UserID string `json:"userId"`
}

// jwtProfileSource implements the JWT profile grant (RFC 7523) and caches the
// resulting access token.
type jwtProfileSource struct {
	key      ServiceAccountKey
	priv     *rsa.PrivateKey
	issuer   string
	tokenURL string
	host     string
	http     *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewTokenSource builds a TokenSource from raw credentials: a service account
// JSON key or a personal access token.
func NewTokenSource(raw []byte, issuer, apiURL, host string, httpClient *http.Client) (TokenSource, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("credentials are empty")
	}
	if !strings.HasPrefix(trimmed, "{") {
		return staticToken(trimmed), nil
	}
	var key ServiceAccountKey
	if err := json.Unmarshal([]byte(trimmed), &key); err != nil {
		return nil, fmt.Errorf("parsing service account key: %w", err)
	}
	if key.KeyID == "" || key.Key == "" || key.UserID == "" {
		return nil, fmt.Errorf("service account key must contain keyId, key and userId")
	}
	priv, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key.Key))
	if err != nil {
		return nil, fmt.Errorf("parsing service account private key: %w", err)
	}
	return &jwtProfileSource{
		key:      key,
		priv:     priv,
		issuer:   issuer,
		tokenURL: strings.TrimSuffix(apiURL, "/") + "/oauth/v2/token",
		host:     host,
		http:     httpClient,
	}, nil
}

func (s *jwtProfileSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Add(time.Minute).Before(s.expiry) {
		return s.token, nil
	}

	now := time.Now()
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    s.key.UserID,
		Subject:   s.key.UserID,
		Audience:  jwt.ClaimStrings{s.issuer},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	})
	assertion.Header["kid"] = s.key.KeyID
	signed, err := assertion.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("signing assertion: %w", err)
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"scope":      {zitadelAPIScope},
		"assertion":  {signed},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if s.host != "" {
		req.Host = s.host
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", fmt.Errorf("token request failed (HTTP %d): %s %s", resp.StatusCode, body.Error, body.ErrorDescription)
	}
	s.token = body.AccessToken
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 300
	}
	s.expiry = now.Add(time.Duration(body.ExpiresIn) * time.Second)
	return s.token, nil
}
