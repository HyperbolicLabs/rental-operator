/*
Copyright 2026 Hyperbolic Labs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hyperbolic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoint is the canonical public API base URL.
const DefaultEndpoint = "https://api.hyperbolic.ai"

// Client is a minimal Hyperbolic public-API client.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// NewClient builds a client for the given endpoint (empty → DefaultEndpoint)
// and API key.
func NewClient(endpoint, apiKey string, opts ...Option) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	c := &Client{
		baseURL: strings.TrimRight(endpoint, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Body
	}
	if e.Code != "" {
		return fmt.Sprintf("hyperbolic API error %d (%s): %s", e.StatusCode, e.Code, msg)
	}
	return fmt.Sprintf("hyperbolic API error %d: %s", e.StatusCode, msg)
}

// hasCode reports whether the error carries the given API error code, matching
// either the parsed code or the raw body (tRPC error shapes vary, so we also
// substring-match as a fallback).
func (e *APIError) hasCode(code string) bool {
	if strings.EqualFold(e.Code, code) {
		return true
	}
	return strings.Contains(e.Body, code)
}

// HasCode is a convenience for callers holding a plain error.
func HasCode(err error, code string) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.hasCode(code)
	}
	return false
}

// StatusCodeOf returns the HTTP status of an APIError, or 0.
func StatusCodeOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// do performs a request and decodes a JSON response into out (may be nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, respBody)
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return nil
}

// parseAPIError extracts a code/message from the various tRPC error shapes.
func parseAPIError(status int, body []byte) *APIError {
	e := &APIError{StatusCode: status, Body: string(body)}

	// Try a few known shapes without failing if none match.
	var flat struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &flat) == nil {
		switch {
		case flat.Code != "":
			e.Code = flat.Code
		case flat.Error.Data.Code != "":
			e.Code = flat.Error.Data.Code
		case flat.Error.Code != "":
			e.Code = flat.Error.Code
		}
		switch {
		case flat.Message != "":
			e.Message = flat.Message
		case flat.Error.Message != "":
			e.Message = flat.Error.Message
		}
	}
	return e
}

// GetBalance returns the account balance. GET /v2/customer/balance.
func (c *Client) GetBalance(ctx context.Context) (*Balance, error) {
	var b Balance
	if err := c.do(ctx, http.MethodGet, "/v2/customer/balance", nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// GetRentalOptions returns the marketplace catalogue. GET /v2/on-demand/rental-options.
// Unauthenticated on the server, but sending the key is harmless.
func (c *Client) GetRentalOptions(ctx context.Context) ([]RentalOption, error) {
	var opts []RentalOption
	if err := c.do(ctx, http.MethodGet, "/v2/on-demand/rental-options", nil, &opts); err != nil {
		return nil, err
	}
	return opts, nil
}

// ListVMRentals returns the account's active VM rentals with live status.
// GET /v2/on-demand/virtual-machine-rentals.
func (c *Client) ListVMRentals(ctx context.Context) ([]VMRental, error) {
	var rentals []VMRental
	if err := c.do(ctx, http.MethodGet, "/v2/on-demand/virtual-machine-rentals", nil, &rentals); err != nil {
		return nil, err
	}
	return rentals, nil
}

// CreateVMRental creates a rental. POST /v2/on-demand/virtual-machine-rentals.
// The returned row carries the ids but NOT live status.
func (c *Client) CreateVMRental(ctx context.Context, req CreateVMRentalRequest) (*RentalRow, error) {
	if req.TermType == "" {
		req.TermType = "on-demand"
	}
	var row RentalRow
	if err := c.do(ctx, http.MethodPost, "/v2/on-demand/virtual-machine-rentals", req, &row); err != nil {
		return nil, err
	}
	return &row, nil
}

// TerminateVMRental terminates by numeric rental id.
// POST /v2/on-demand/virtual-machine-rentals/terminate.
func (c *Client) TerminateVMRental(ctx context.Context, numericID int64, reason string) error {
	return c.do(ctx, http.MethodPost, "/v2/on-demand/virtual-machine-rentals/terminate",
		terminateRequest{RentalID: numericID, Reason: reason}, nil)
}

// RebootInstance reboots one node by numeric rental id + instanceRef.
// POST /v2/on-demand/virtual-machine-rentals/reboot-instance.
func (c *Client) RebootInstance(ctx context.Context, numericID int64, instanceRef string) error {
	return c.do(ctx, http.MethodPost, "/v2/on-demand/virtual-machine-rentals/reboot-instance",
		rebootRequest{RentalID: numericID, InstanceRef: instanceRef}, nil)
}
