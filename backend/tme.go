package main

// TME pricing provider — implements pricingProvider against the TME API.
//
// TME is keyed by its own catalogue "Symbol", which usually but not always
// equals the manufacturer MPN, so this is a two-step flow: Search.json to
// resolve the MPN to a Symbol, then GetPricesAndStocks.json for the price
// ladder + stock. Both calls are HMAC-SHA1 signed.
//
// The signature is the security-critical part. TME's contract:
//
//	base      = METHOD + "&" + rawEncode(url) + "&" + rawEncode(sortedParams)
//	signature = base64( HMAC-SHA1( appSecret, base ) )
//
// where sortedParams is the params (minus ApiSignature) sorted by key and
// joined as k=rawEncode(v)&… , and rawEncode is RFC3986 (PHP rawurlencode):
// space → %20, only A-Za-z0-9-_.~ left literal. Go's url.QueryEscape does
// NOT match (space → +, escapes ~), which is why rawEncode is hand-rolled.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const tmeBaseURL = "https://api.tme.eu"

type tmeProvider struct {
	token      string // TME API token (public id)
	appSecret  string // TME app secret (HMAC key)
	baseURL    string // overridable for tests
	httpClient *http.Client
}

func newTMEProvider(token, appSecret string) *tmeProvider {
	return &tmeProvider{
		token:      token,
		appSecret:  appSecret,
		baseURL:    tmeBaseURL,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (tp *tmeProvider) name() string { return "tme" }

func (tp *tmeProvider) priceByMPN(ctx context.Context, mpn, currency string) ([]SupplierOffer, error) {
	mpn = strings.TrimSpace(mpn)
	if mpn == "" {
		return nil, nil
	}

	// Step 1: resolve the MPN to a TME Symbol.
	searchParams := url.Values{}
	searchParams.Set("Token", tp.token)
	searchParams.Set("Country", "GB")
	searchParams.Set("Language", "EN")
	searchParams.Set("SearchPlain", mpn)
	var search tmeSearchResponse
	if err := tp.call(ctx, "/Products/Search.json", searchParams, &search); err != nil {
		return nil, err
	}
	if search.Status != "OK" {
		return nil, fmt.Errorf("tme: search status %q: %s", search.Status, search.Error)
	}
	if len(search.Data.ProductList) == 0 {
		return nil, nil
	}
	// Take the first symbol (best match). One symbol keeps the second call
	// — and its quota cost — bounded.
	symbol := search.Data.ProductList[0].Symbol
	if symbol == "" {
		return nil, nil
	}

	// Step 2: prices + stock for the resolved symbol.
	priceParams := url.Values{}
	priceParams.Set("Token", tp.token)
	priceParams.Set("Country", "GB")
	priceParams.Set("Language", "EN")
	priceParams.Set("Currency", currency)
	priceParams.Set("SymbolList[0]", symbol)
	var prices tmePricesResponse
	if err := tp.call(ctx, "/Products/GetPricesAndStocks.json", priceParams, &prices); err != nil {
		return nil, err
	}
	if prices.Status != "OK" {
		return nil, fmt.Errorf("tme: prices status %q: %s", prices.Status, prices.Error)
	}

	respCurrency := currency
	if prices.Data.Currency != "" {
		respCurrency = prices.Data.Currency
	}
	now := time.Now().UTC()
	var offers []SupplierOffer
	for _, p := range prices.Data.ProductList {
		if len(p.PriceList) == 0 {
			continue
		}
		out := SupplierOffer{
			Supplier:    normaliseSupplierName("TME"),
			SKU:         p.Symbol,
			SupplierURL: fmt.Sprintf("https://www.tme.eu/en/details/%s/", p.Symbol),
			Source:      "tme",
			Currency:    respCurrency,
			FetchedAt:   now,
		}
		if p.AmountInStock > 0 {
			s := p.AmountInStock
			out.Stock = &s
		}
		for _, pr := range p.PriceList {
			out.PriceBreaks = append(out.PriceBreaks, PriceBreak{
				Quantity: pr.Amount,
				Price:    pr.PriceValue,
			})
		}
		offers = append(offers, out)
	}
	return offers, nil
}

// call signs and POSTs a TME request, decoding the JSON response into out.
func (tp *tmeProvider) call(ctx context.Context, path string, params url.Values, out any) error {
	apiURL := tp.baseURL + path
	params.Set("ApiSignature", tmeSign(tp.appSecret, tmeSignatureBase(http.MethodPost, apiURL, params)))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("tme: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := tp.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tme: request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tme: status %d: %s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tme: decode: %w", err)
	}
	return nil
}

// tmeSignatureBase builds the string TME signs: METHOD, raw-encoded URL,
// and the params (excluding ApiSignature) sorted by key and raw-encoded,
// the whole param string raw-encoded once more, joined by '&'.
func tmeSignatureBase(method, apiURL string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "ApiSignature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, rawEncode(k)+"="+rawEncode(params.Get(k)))
	}
	sortedParams := strings.Join(parts, "&")
	return method + "&" + rawEncode(apiURL) + "&" + rawEncode(sortedParams)
}

// tmeSign is base64( HMAC-SHA1( secret, base ) ).
func tmeSign(secret, base string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(base))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// rawEncode matches PHP rawurlencode (RFC3986): every byte except
// A-Za-z0-9 and -_.~ is %XX-escaped; space becomes %20, not '+'.
func rawEncode(s string) string {
	const upper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upper[c>>4])
			b.WriteByte(upper[c&0x0F])
		}
	}
	return b.String()
}

type tmeSearchResponse struct {
	Status string `json:"Status"`
	Error  string `json:"Error"`
	Data   struct {
		ProductList []struct {
			Symbol         string `json:"Symbol"`
			OriginalSymbol string `json:"OriginalSymbol"`
			Producer       string `json:"Producer"`
		} `json:"ProductList"`
	} `json:"Data"`
}

type tmePricesResponse struct {
	Status string `json:"Status"`
	Error  string `json:"Error"`
	Data   struct {
		Currency    string `json:"Currency"`
		ProductList []struct {
			Symbol        string `json:"Symbol"`
			Unit          string `json:"Unit"`
			AmountInStock int    `json:"AmountInStock"`
			PriceList     []struct {
				Amount     int     `json:"Amount"`
				PriceValue float64 `json:"PriceValue"`
			} `json:"PriceList"`
		} `json:"ProductList"`
	} `json:"Data"`
}
