package companyfund

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *AirwallexClient) endpoint(path string, query url.Values) *url.URL {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawPath = ""
	if query == nil {
		endpoint.RawQuery = ""
	} else {
		endpoint.RawQuery = query.Encode()
	}
	return &endpoint
}

func (c *AirwallexClient) do(ctx context.Context, method string, endpoint *url.URL, headers http.Header) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create airwallex request: %w", err)
	}
	request.Header = headers.Clone()
	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, ErrAirwallexNetwork
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &AirwallexHTTPError{StatusCode: response.StatusCode}
	}
	return readAirwallexResponseBody(response.Body)
}

func readAirwallexResponseBody(body io.Reader) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(body, maxAirwallexResponseBytes+1))
	if err != nil {
		return nil, ErrAirwallexResponseRead
	}
	if len(contents) > maxAirwallexResponseBytes {
		return nil, ErrAirwallexResponseTooLarge
	}
	return contents, nil
}

func decodeAirwallexJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return ErrAirwallexMalformedResponse
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrAirwallexMalformedResponse
	}
	return nil
}
