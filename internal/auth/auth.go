package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/jon-cooper/skeetdelete/internal/types"
)

type Auth struct {
	mu         sync.Mutex
	session    *types.Session
	client     *xrpc.Client
	httpClient *http.Client
}

func NewAuth() *Auth {
	return &Auth{
		httpClient: &http.Client{Timeout: 2 * time.Minute},
		client: &xrpc.Client{
			Host:   "https://bsky.social",
			Client: &http.Client{Timeout: 2 * time.Minute},
		},
	}
}

func (a *Auth) CreateSession(ctx context.Context, identifier, password string) (*types.Session, error) {
	pdsHost := "https://bsky.social"

	a.mu.Lock()
	out, err := atproto.ServerCreateSession(ctx, a.client, &atproto.ServerCreateSession_Input{
		Identifier: identifier,
		Password:   password,
	})
	if err != nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("creating session: %w", err)
	}

	a.client.Auth = &xrpc.AuthInfo{
		AccessJwt:  out.AccessJwt,
		RefreshJwt: out.RefreshJwt,
		Handle:     out.Handle,
		Did:        out.Did,
	}
	a.mu.Unlock()

	resolved, err := a.resolvePDSHost(ctx, out.Did)
	if err == nil && resolved != "" {
		pdsHost = resolved
	}

	a.mu.Lock()
	if pdsHost != "https://bsky.social" {
		a.client.Host = pdsHost
	}

	session := &types.Session{
		Did:        out.Did,
		Handle:     out.Handle,
		AccessJwt:  out.AccessJwt,
		RefreshJwt: out.RefreshJwt,
		PDSHost:    pdsHost,
	}
	a.session = session
	a.mu.Unlock()
	return session, nil
}

func (a *Auth) RefreshSession(ctx context.Context) (*types.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.session == nil {
		return nil, fmt.Errorf("no session to refresh")
	}

	savedAccessJwt := a.client.Auth.AccessJwt
	a.client.Auth.AccessJwt = a.client.Auth.RefreshJwt

	out, err := atproto.ServerRefreshSession(ctx, a.client)
	if err != nil {
		a.client.Auth.AccessJwt = savedAccessJwt
		return nil, fmt.Errorf("refreshing session: %w", err)
	}

	a.client.Auth = &xrpc.AuthInfo{
		AccessJwt:  out.AccessJwt,
		RefreshJwt: out.RefreshJwt,
		Handle:     out.Handle,
		Did:        out.Did,
	}

	newSession := &types.Session{
		Did:        out.Did,
		Handle:     out.Handle,
		AccessJwt:  out.AccessJwt,
		RefreshJwt: out.RefreshJwt,
		PDSHost:    a.session.PDSHost,
	}
	a.session = newSession

	return newSession, nil
}

func (a *Auth) GetSession() *types.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session
}

func (a *Auth) SetSession(s *types.Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.session = s
	if s != nil {
		a.client.Auth = &xrpc.AuthInfo{
			AccessJwt:  s.AccessJwt,
			RefreshJwt: s.RefreshJwt,
			Handle:     s.Handle,
			Did:        s.Did,
		}
		if s.PDSHost != "" {
			a.client.Host = s.PDSHost
		}
	} else {
		a.client.Auth = nil
	}
}

func (a *Auth) IsAuthenticated() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session != nil && a.session.AccessJwt != ""
}

func (a *Auth) WithClient(fn func(*xrpc.Client) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return fn(a.client)
}

func (a *Auth) resolvePDSHost(ctx context.Context, did string) (string, error) {
	if len(did) < 9 {
		return "", fmt.Errorf("invalid DID format")
	}

	switch {
	case len(did) > 8 && did[:8] == "did:plc:":
		return a.resolvePLCHost(ctx, did)
	case len(did) > 8 && did[:8] == "did:web:":
		return a.resolveWebHost(ctx, did[8:])
	default:
		return "https://bsky.social", nil
	}
}

type plcDirectoryResponse struct {
	Service []struct {
		Type            string `json:"type"`
		ServiceEndpoint string `json:"serviceEndpoint"`
	} `json:"service"`
}

func (a *Auth) resolvePLCHost(ctx context.Context, did string) (string, error) {
	url := fmt.Sprintf("https://plc.directory/%s", did)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var plcResp plcDirectoryResponse
	if err := json.Unmarshal(body, &plcResp); err != nil {
		return "", err
	}

	for _, svc := range plcResp.Service {
		if svc.Type == "AtprotoPersonalDataServer" && svc.ServiceEndpoint != "" {
			return svc.ServiceEndpoint, nil
		}
	}

	if len(plcResp.Service) > 0 && plcResp.Service[0].ServiceEndpoint != "" {
		return plcResp.Service[0].ServiceEndpoint, nil
	}

	return "", fmt.Errorf("no PDS service found in PLC directory")
}

func (a *Auth) resolveWebHost(ctx context.Context, domain string) (string, error) {
	url := fmt.Sprintf("https://%s/.well-known/did.json", domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var didDoc struct {
		Service []struct {
			Type            string `json:"type"`
			ServiceEndpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := json.Unmarshal(body, &didDoc); err != nil {
		return "", err
	}

	for _, svc := range didDoc.Service {
		if svc.ServiceEndpoint != "" {
			return svc.ServiceEndpoint, nil
		}
	}

	return "", fmt.Errorf("no service endpoint found in did.json")
}