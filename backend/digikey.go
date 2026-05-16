package main

// Digi-Key pricing provider — implements pricingProvider against the
// Product Information API v4. Auth is OAuth2 client-credentials with
// ~10-minute tokens, so the refresh buffer is tighter than the other
// providers'. Locale + currency + client-id travel as X-DIGIKEY-* headers,
// not query params — omitting them silently yields USD prices or a 401.
//
// v4 nests pricing under ProductVariations (packaging types: cut-tape,
// reel, tube), each with its own DigiKeyProductNumber and StandardPricing
// ladder. We emit one SupplierOffer per variation so the
// (mpn, supplier, sku, currency) cache key keeps them apart.

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
	"sync"
	"time"
)

const (
	digikeyTokenEndpoint  = "https://api.digikey.com/v1/oauth2/token"
	digikeySearchEndpoint = "https://api.digikey.com/products/v4/search/keyword"
	// Digi-Key access tokens live ~599s. Refresh well inside that so a
	// multi-row pricing run never trips an expiry mid-flight.
	digikeyTokenRefreshBuffer = 60 * time.Second
	digikeyLocaleSite         = "UK"
)

type digikeyProvider struct {
	clientID     string
	clientSecret string
	tokenURL     string // overridable for tests
	searchURL    string // overridable for tests
	httpClient   *http.Client

	tokenMu      sync.Mutex
	cachedToken  string
	tokenExpires time.Time
}

func newDigikeyProvider(clientID, clientSecret string) *digikeyProvider {
	return &digikeyProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     digikeyTokenEndpoint,
		searchURL:    digikeySearchEndpoint,
		httpClient:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (d *digikeyProvider) name() string { return "digikey" }

func (d *digikeyProvider) priceByMPN(ctx context.Context, mpn, currency string) ([]SupplierOffer, error) {
	mpn = strings.TrimSpace(mpn)
	if mpn == "" {
		return nil, nil
	}
	token, err := d.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("digikey: token: %w", err)
	}

	reqBody, _ := json.Marshal(map[string]any{
		"Keywords": mpn,
		"Limit":    10,
		"Offset":   0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.searchURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("digikey: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-DIGIKEY-Client-Id", d.clientID)
	req.Header.Set("X-DIGIKEY-Locale-Site", digikeyLocaleSite)
	req.Header.Set("X-DIGIKEY-Locale-Currency", currency)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("digikey: request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("digikey: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed digikeySearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("digikey: decode: %w", err)
	}
	if len(parsed.Products) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	want := strings.ToUpper(mpn)
	var offers []SupplierOffer
	for _, p := range parsed.Products {
		// Digi-Key suffixes the packaging code onto the MPN — a search for
		// "STM32F103C8T6" returns both "STM32F103C8T6" (Tray, often 0 stock)
		// and "STM32F103C8T6TR" (the in-stock reel). Exact equality would
		// throw away the stocked product, so accept the requested MPN or
		// anything that extends it. (Rejects unrelated "…T7".)
		got := strings.ToUpper(strings.TrimSpace(p.ManufacturerProductNumber))
		if got != want && !strings.HasPrefix(got, want) {
			continue
		}
		lead := parseLeadWeeksToDays(p.ManufacturerLeadWeeks)
		for _, v := range p.ProductVariations {
			if len(v.StandardPricing) == 0 {
				continue
			}
			// Per-packaging stock, not the product-level total — a Tray
			// variation can be 0 while the reel has thousands.
			stock := v.QuantityAvailableForPackageType
			out := SupplierOffer{
				Supplier:     normaliseSupplierName("DigiKey"),
				SKU:          v.DigiKeyProductNumber,
				SupplierURL:  p.ProductURL,
				Source:       "digikey",
				Currency:     currency,
				FetchedAt:    now,
				Stock:        &stock,
				LeadTimeDays: lead,
			}
			for _, sp := range v.StandardPricing {
				out.PriceBreaks = append(out.PriceBreaks, PriceBreak{
					Quantity: sp.BreakQuantity,
					Price:    sp.UnitPrice,
				})
			}
			offers = append(offers, out)
		}
	}
	return offers, nil
}

func (d *digikeyProvider) getToken(ctx context.Context) (string, error) {
	d.tokenMu.Lock()
	defer d.tokenMu.Unlock()
	if d.cachedToken != "" && time.Until(d.tokenExpires) > digikeyTokenRefreshBuffer {
		return d.cachedToken, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {d.clientID},
		"client_secret": {d.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token status %d: %s", resp.StatusCode, string(b))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", errors.New("digikey: empty access_token in response")
	}
	d.cachedToken = tok.AccessToken
	d.tokenExpires = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return d.cachedToken, nil
}

// parseLeadWeeksToDays converts Digi-Key's ManufacturerLeadWeeks field
// (a bare number string like "30", meaning 30 *weeks*) into days. The
// field name is the unit — always ×7. Returns nil when unparseable.
func parseLeadWeeksToDays(s string) *int {
	weeks := parseLeadingInt(s)
	if weeks == nil {
		return nil
	}
	days := *weeks * 7
	return &days
}

type digikeySearchResponse struct {
	Products []struct {
		ManufacturerProductNumber string `json:"ManufacturerProductNumber"`
		Manufacturer              struct {
			Name string `json:"Name"`
		} `json:"Manufacturer"`
		ProductURL            string `json:"ProductUrl"`
		ManufacturerLeadWeeks string `json:"ManufacturerLeadWeeks"`
		ProductVariations     []struct {
			DigiKeyProductNumber string `json:"DigiKeyProductNumber"`
			PackageType          struct {
				Name string `json:"Name"`
			} `json:"PackageType"`
			// Stock is per packaging variation (TR/CT/Digi-Reel/Tray),
			// NOT the product-level QuantityAvailable — the product total
			// is misleading when one packaging is in stock and another
			// (e.g. Tray) is zero.
			QuantityAvailableForPackageType int `json:"QuantityAvailableforPackageType"`
			StandardPricing                 []struct {
				BreakQuantity int     `json:"BreakQuantity"`
				UnitPrice     float64 `json:"UnitPrice"`
			} `json:"StandardPricing"`
		} `json:"ProductVariations"`
	} `json:"Products"`
}
