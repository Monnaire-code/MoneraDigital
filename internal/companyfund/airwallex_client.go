package companyfund

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type airwallexAccessToken struct {
	value      string
	expiresAt  time.Time
	generation uint64
}

type airwallexTokenFlight struct {
	done  chan struct{}
	token airwallexAccessToken
	err   error
}

// AirwallexClient is a narrow backend-only REST client. It owns only an
// in-process access token; ingestion, storage, and webhook handling stay out
// of this boundary.
type AirwallexClient struct {
	baseURL    *url.URL
	clientID   string
	apiKey     string
	apiVersion string
	loginAs    string
	httpClient *http.Client
	now        func() time.Time
	skew       time.Duration

	tokenMu         sync.Mutex
	token           airwallexAccessToken
	flight          *airwallexTokenFlight
	tokenGeneration uint64
}

func NewAirwallexClient(config AirwallexClientConfig) (*AirwallexClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = AirwallexDefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("airwallex base URL must be an absolute https URL without credentials, query, or fragment")
	}
	clientID := strings.TrimSpace(config.ClientID)
	apiKey := strings.TrimSpace(config.APIKey)
	apiVersion, err := parseAirwallexAPIVersion(config.APIVersion)
	if err != nil {
		return nil, err
	}
	loginAs := strings.TrimSpace(config.LoginAs)
	if clientID == "" || apiKey == "" {
		return nil, fmt.Errorf("airwallex client ID and API key are required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultAirwallexHTTPTimeout}
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	skew := config.TokenRefreshSkew
	if skew == 0 {
		skew = defaultAirwallexTokenRefreshSkew
	}
	if skew < 0 {
		return nil, fmt.Errorf("airwallex token refresh skew must not be negative")
	}
	return &AirwallexClient{
		baseURL:    parsed,
		clientID:   clientID,
		apiKey:     apiKey,
		apiVersion: apiVersion,
		loginAs:    loginAs,
		httpClient: airwallexHTTPClientWithoutRedirects(httpClient),
		now:        now,
		skew:       skew,
	}, nil
}

// PinnedAPIVersion returns the non-secret business API contract pin used by
// authenticated requests. Reconcilers use it to reject a mismatched sync-run
// input before asking the provider for financial transaction facts.
func (c *AirwallexClient) PinnedAPIVersion() string {
	if c == nil {
		return ""
	}
	return c.apiVersion
}

// PinnedLoginAsScope returns the exact x-login-as identity attached to every
// authenticated business request. A reconciler requires a non-empty value;
// the base client keeps it optional because it is also used by endpoints that
// are not company-fund reconciliation.
func (c *AirwallexClient) PinnedLoginAsScope() string {
	if c == nil {
		return ""
	}
	return c.loginAs
}

func airwallexHTTPClientWithoutRedirects(source *http.Client) *http.Client {
	clone := *source
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func (c *AirwallexClient) accessToken(ctx context.Context) (airwallexAccessToken, error) {
	if c == nil || c.baseURL == nil || c.httpClient == nil || c.now == nil || c.apiVersion == "" {
		return airwallexAccessToken{}, fmt.Errorf("airwallex client is not configured")
	}
	if ctx == nil {
		return airwallexAccessToken{}, fmt.Errorf("airwallex token context is required")
	}
	now := c.now().UTC()
	c.tokenMu.Lock()
	if c.tokenUsable(now) {
		token := c.token
		c.tokenMu.Unlock()
		return token, nil
	}
	if c.flight != nil {
		flight := c.flight
		c.tokenMu.Unlock()
		select {
		case <-flight.done:
			return flight.token, flight.err
		case <-ctx.Done():
			return airwallexAccessToken{}, ctx.Err()
		}
	}
	flight := &airwallexTokenFlight{done: make(chan struct{})}
	c.flight = flight
	c.tokenMu.Unlock()

	token, err := c.login(ctx)
	c.tokenMu.Lock()
	if err == nil {
		c.tokenGeneration++
		token.generation = c.tokenGeneration
		c.token = token
		flight.token = token
	} else {
		flight.err = err
	}
	c.flight = nil
	close(flight.done)
	c.tokenMu.Unlock()
	return flight.token, flight.err
}

// authenticatedGET retries only one idempotent business GET after a provider
// rejects its bearer token. Long-lived credentials are used exclusively by the
// login call; a second 401/403 is returned without another retry.
func (c *AirwallexClient) authenticatedGET(ctx context.Context, endpoint *url.URL) ([]byte, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("airwallex GET endpoint is required")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.getWithAccessToken(ctx, endpoint, token)
	if !errors.Is(err, ErrAirwallexUnauthorized) {
		return body, err
	}

	// Invalidate only the exact token snapshot that failed. A concurrent refresh
	// can install a newer token before this response arrives; it must survive.
	c.invalidateAccessToken(token)
	refreshedToken, refreshErr := c.accessToken(ctx)
	if refreshErr != nil {
		return nil, refreshErr
	}
	body, err = c.getWithAccessToken(ctx, endpoint, refreshedToken)
	if errors.Is(err, ErrAirwallexUnauthorized) {
		c.invalidateAccessToken(refreshedToken)
	}
	return body, err
}

func (c *AirwallexClient) getWithAccessToken(ctx context.Context, endpoint *url.URL, token airwallexAccessToken) ([]byte, error) {
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("Authorization", "Bearer "+token.value)
	headers.Set("x-api-version", c.apiVersion)
	return c.do(ctx, http.MethodGet, endpoint, headers)
}

func (c *AirwallexClient) invalidateAccessToken(rejected airwallexAccessToken) {
	if c == nil || rejected.value == "" {
		return
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token.generation == rejected.generation && c.token.value == rejected.value {
		c.token = airwallexAccessToken{}
	}
}

func (c *AirwallexClient) tokenUsable(now time.Time) bool {
	return c.token.value != "" && now.Before(c.token.expiresAt.Add(-c.skew))
}

func (c *AirwallexClient) login(ctx context.Context) (airwallexAccessToken, error) {
	endpoint := c.endpoint("/api/v1/authentication/login", nil)
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("Content-Type", "application/json")
	headers.Set("x-client-id", c.clientID)
	headers.Set("x-api-key", c.apiKey)
	if c.loginAs != "" {
		headers.Set("x-login-as", c.loginAs)
	}
	body, err := c.do(ctx, http.MethodPost, endpoint, headers)
	if err != nil {
		return airwallexAccessToken{}, err
	}
	var response struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := decodeAirwallexJSON(body, &response); err != nil {
		return airwallexAccessToken{}, err
	}
	token := strings.TrimSpace(response.Token)
	expiresAt, err := parseAirwallexTokenExpiry(response.ExpiresAt)
	if token == "" || err != nil || !expiresAt.After(c.now().UTC()) {
		return airwallexAccessToken{}, ErrAirwallexMalformedResponse
	}
	return airwallexAccessToken{value: token, expiresAt: expiresAt.UTC()}, nil
}
