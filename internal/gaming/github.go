// Package gaming writes bounded desired-state changes to Git, never Kubernetes.
package gaming

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var ErrConflict = errors.New("Git a changé ; rechargez les serveurs avant de réessayer")
var ErrCredentials = errors.New("l’accès d’écriture GitHub du portail n’est pas configuré")

type TokenSource interface {
	Token(context.Context) (string, error)
}

type GitHub struct {
	Client  *http.Client
	Tokens  TokenSource
	baseURL string
}

func NewGitHub(tokens TokenSource) *GitHub {
	return &GitHub{Tokens: tokens, baseURL: "https://api.github.com", Client: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (g *GitHub) call(ctx context.Context, method, path string, input, output any) error {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, &body)
	if err != nil {
		return errors.New("requête GitHub invalide")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Tokens != nil {
		token, err := g.Tokens.Token(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else if method != http.MethodGet {
		return ErrCredentials
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return errors.New("GitHub ne répond pas")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 409 || resp.StatusCode == 422 {
		return ErrConflict
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub a refusé l’opération (HTTP %d)", resp.StatusCode)
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(output); err != nil {
		return errors.New("réponse GitHub invalide")
	}
	return nil
}

// AppTokens keeps short-lived installation tokens in memory. No token is sent
// to the browser or logged. GitHub permissions are narrowed to one repository.
type AppTokens struct {
	AppID, InstallationID int64
	Repository            string
	key                   *rsa.PrivateKey
	client                *http.Client
	mu                    sync.Mutex
	token                 string
	expires               time.Time
}

func NewAppTokens(appID, installationID int64, repository string, keyPEM []byte) (*AppTokens, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil || appID <= 0 || installationID <= 0 {
		return nil, errors.New("configuration GitHub App invalide")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		value, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return nil, errors.New("clé GitHub App invalide")
		}
		var ok bool
		key, ok = value.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("une clé RSA GitHub App est requise")
		}
	}
	return &AppTokens{AppID: appID, InstallationID: installationID, Repository: repository, key: key,
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *AppTokens) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Until(a.expires) > time.Minute {
		return a.token, nil
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-30 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(a.AppID, 10)})
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", ErrCredentials
	}
	body, _ := json.Marshal(map[string]any{"repositories": []string{a.Repository}, "permissions": map[string]string{"contents": "write", "pull_requests": "write"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/app/installations/"+strconv.FormatInt(a.InstallationID, 10)+"/access_tokens", bytes.NewReader(body))
	if err != nil {
		return "", ErrCredentials
	}
	req.Header.Set("Authorization", "Bearer "+unsigned+"."+base64.RawURLEncoding.EncodeToString(sig))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.New("authentification GitHub indisponible")
	}
	defer resp.Body.Close()
	var value struct {
		Token   string    `json:"token"`
		Expires time.Time `json:"expires_at"`
	}
	if resp.StatusCode != 201 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&value) != nil || value.Token == "" || !value.Expires.After(now.Add(time.Minute)) {
		return "", ErrCredentials
	}
	a.token, a.expires = value.Token, value.Expires
	return a.token, nil
}
