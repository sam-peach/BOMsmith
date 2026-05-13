package main

// Nexar / Octopart pricing provider — production implementation of
// pricingProvider. OAuth Client Credentials → bearer token → one GraphQL
// query per MPN against api.nexar.com.
//
// Token caching: tokens are valid 24h; cache in-memory with a 1h refresh
// buffer so a process restart doesn't dump us into uncached territory.
// Concurrent callers share the token via a sync.Mutex (we only ever fetch
// one token at a time).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	nexarTokenEndpoint   = "https://identity.nexar.com/connect/token"
	nexarGraphQLEndpoint = "https://api.nexar.com/graphql"
	// nexarSupplyScope is the OAuth scope required for supSearchMpn and the
	// rest of the Supply API. Without it the token issues but the GraphQL
	// query returns a permission error. Per Nexar docs (Authorization).
	nexarSupplyScope = "supply.domain"
	// Refresh tokens this long before expiry — keeps long-lived processes
	// from racing the expiry edge. Nexar tokens are valid 24h.
	nexarTokenRefreshBuffer = 1 * time.Hour
)

// nexarProvider implements pricingProvider against the Nexar Supply API.
type nexarProvider struct {
	clientID     string
	clientSecret string
	tokenURL     string // overridable for tests
	graphqlURL   string // overridable for tests
	httpClient   *http.Client

	tokenMu       sync.Mutex
	cachedToken   string
	tokenExpires  time.Time
}

func newNexarProvider(clientID, clientSecret string) *nexarProvider {
	return &nexarProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     nexarTokenEndpoint,
		graphqlURL:   nexarGraphQLEndpoint,
		httpClient:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (n *nexarProvider) name() string { return "nexar" }

func (n *nexarProvider) priceByMPN(ctx context.Context, mpn, currency string) ([]SupplierOffer, error) {
	mpn = strings.TrimSpace(mpn)
	if mpn == "" {
		return nil, nil
	}
	token, err := n.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("nexar: token: %w", err)
	}

	body, _ := json.Marshal(map[string]any{
		"query": nexarSearchMPNQuery,
		"variables": map[string]any{
			"mpn":      mpn,
			"currency": currency,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.graphqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("nexar: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nexar: graphql: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nexar: graphql status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed nexarSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("nexar: decode: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("nexar: graphql errors: %s", parsed.Errors[0].Message)
	}
	if len(parsed.Data.SupSearchMPN.Results) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	var offers []SupplierOffer
	for _, result := range parsed.Data.SupSearchMPN.Results {
		for _, seller := range result.Part.Sellers {
			for _, offer := range seller.Offers {
				if len(offer.Prices) == 0 {
					continue
				}
				out := SupplierOffer{
					Supplier:    normaliseSupplierName(seller.Company.Name),
					SKU:         offer.SKU,
					SupplierURL: offer.ClickURL,
					Source:      "nexar",
					Currency:    currency,
					FetchedAt:   now,
				}
				if offer.InventoryLevel != nil {
					v := *offer.InventoryLevel
					out.Stock = &v
				}
				if offer.FactoryLeadDays != nil {
					v := *offer.FactoryLeadDays
					out.LeadTimeDays = &v
				}
				for _, p := range offer.Prices {
					out.PriceBreaks = append(out.PriceBreaks, PriceBreak{
						Quantity: p.Quantity,
						Price:    p.ConvertedPrice,
					})
				}
				offers = append(offers, out)
			}
		}
	}
	return offers, nil
}

// getToken returns a cached bearer token, refreshing if it's missing or
// within the refresh buffer of expiry. Concurrent callers serialise on the
// mutex — we never want two parallel token fetches.
func (n *nexarProvider) getToken(ctx context.Context) (string, error) {
	n.tokenMu.Lock()
	defer n.tokenMu.Unlock()
	if n.cachedToken != "" && time.Until(n.tokenExpires) > nexarTokenRefreshBuffer {
		return n.cachedToken, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {n.clientID},
		"client_secret": {n.clientSecret},
		"scope":         {nexarSupplyScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token status %d: %s", resp.StatusCode, string(respBody))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", errors.New("nexar: empty access_token in response")
	}
	n.cachedToken = tok.AccessToken
	n.tokenExpires = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return n.cachedToken, nil
}

// normaliseSupplierName collapses Nexar's verbose display names to the short
// forms used elsewhere in the codebase ("DigiKey", "Farnell" etc.). Brand
// equivalents are merged where they map to the same parent company:
// Premier Farnell sells under Farnell/Newark/element14, Arrow owns Verical,
// and "RS (Formerly Allied Electronics)" is the same RS we already know.
// Unknown names pass through trimmed but otherwise unchanged.
//
// Driven by the live-Nexar test that surfaced names like "element14 APAC",
// "RS (Formerly Allied Electronics)", and "Future Electronics" in raw form —
// without normalisation each shows up as a separate "supplier" in the UI,
// even when Andrew thinks of them as the same vendor.
func normaliseSupplierName(s string) string {
	clean := strings.ToLower(strings.TrimSpace(s))
	switch {
	case matchesAny(clean, "digi-key", "digi-key electronics", "digikey"):
		return "DigiKey"
	case matchesAny(clean, "mouser", "mouser electronics"):
		return "Mouser"
	case matchesAny(clean, "farnell", "element14", "element 14", "farnell element14", "newark") ||
		strings.HasPrefix(clean, "element14 ") || strings.HasPrefix(clean, "element 14 "):
		return "Farnell"
	case matchesAny(clean, "rs", "rs components", "rs pro") ||
		strings.HasPrefix(clean, "rs ("):
		return "RS"
	case matchesAny(clean, "arrow", "arrow electronics", "verical"):
		return "Arrow"
	case matchesAny(clean, "avnet", "avnet abacus", "avnet silica"):
		return "Avnet"
	case matchesAny(clean, "future", "future electronics"):
		return "Future"
	case matchesAny(clean, "tme", "tme electronic components"):
		return "TME"
	case matchesAny(clean, "lcsc", "lcsc electronics"):
		return "LCSC"
	case matchesAny(clean, "conrad", "conrad electronic"):
		return "Conrad"
	default:
		return strings.TrimSpace(s)
	}
}

func matchesAny(s string, options ...string) bool {
	return slices.Contains(options, s)
}

// nexarSearchMPNQuery is the GraphQL query body. Currency conversion is
// set at the query level — every offer's `convertedPrice` field is then
// the price in $currency, and `convertedCurrency` echoes it back.
//
// We deliberately do NOT pass a `country` argument: Andrew's BOMs span
// multiple regions (UK Farnell + US DigiKey + global Mouser) and the
// country filter would drop offers from outside that region.
const nexarSearchMPNQuery = `query PriceByMpn($mpn: String!, $currency: String!) {
  supSearchMpn(q: $mpn, currency: $currency, limit: 1) {
    results {
      part {
        mpn
        manufacturer { name }
        sellers {
          company { name }
          offers {
            sku
            inventoryLevel
            factoryLeadDays
            clickUrl
            prices {
              quantity
              price
              currency
              convertedPrice
              convertedCurrency
            }
          }
        }
      }
    }
  }
}`

type nexarSearchResponse struct {
	Data struct {
		SupSearchMPN struct {
			Results []struct {
				Part struct {
					MPN          string `json:"mpn"`
					Manufacturer struct {
						Name string `json:"name"`
					} `json:"manufacturer"`
					Sellers []struct {
						Company struct {
							Name string `json:"name"`
						} `json:"company"`
						Offers []struct {
							SKU             string `json:"sku"`
							InventoryLevel  *int   `json:"inventoryLevel"`
							FactoryLeadDays *int   `json:"factoryLeadDays"`
							ClickURL        string `json:"clickUrl"`
							Prices          []struct {
								Quantity          int     `json:"quantity"`
								Price             float64 `json:"price"`             // native (seller's currency)
								Currency          string  `json:"currency"`          // native currency
								ConvertedPrice    float64 `json:"convertedPrice"`    // in query's currency
								ConvertedCurrency string  `json:"convertedCurrency"` // = query currency
							} `json:"prices"`
						} `json:"offers"`
					} `json:"sellers"`
				} `json:"part"`
			} `json:"results"`
		} `json:"supSearchMpn"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}
