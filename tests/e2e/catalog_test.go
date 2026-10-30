//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// These tests deliberately import no application packages: only the HTTP
// contract, a real compiled server, and its PostgreSQL database are exercised.
var baseURL string
var phase string
var client = &http.Client{Timeout: 8 * time.Second}

type product struct {
	ID          string    `json:"id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Price       int64     `json:"price_cents"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type page struct {
	Items []product `json:"items"`
	Next  *string   `json:"next_cursor"`
}
type response struct {
	status int
	header http.Header
	body   []byte
}

func TestMain(m *testing.M) {
	baseURL = strings.TrimRight(os.Getenv("E2E_BASE_URL"), "/")
	phase = os.Getenv("E2E_PHASE")
	if phase == "" {
		phase = "normal"
	}
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "E2E_BASE_URL is required")
		os.Exit(2)
	}
	if phase != "normal" && phase != "outage" && phase != "recovery" {
		fmt.Fprintln(os.Stderr, "invalid E2E_PHASE")
		os.Exit(2)
	}
	if phase != "outage" {
		ready := false
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			r, err := client.Get(baseURL + "/readyz")
			if err == nil {
				_ = r.Body.Close()
				if r.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		if !ready {
			fmt.Fprintln(os.Stderr, "API did not become ready within 60 seconds")
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func only(t *testing.T, want string) {
	t.Helper()
	if phase != want {
		t.Skip("scenario runs in " + want + " phase")
	}
}

func call(t *testing.T, method, path string, body []byte, media string) response {
	t.Helper()
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
	require.NoError(t, err)
	if media != "" {
		req.Header.Set("Content-Type", media)
	}
	r, err := client.Do(req)
	require.NoError(t, err)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	require.NotEmpty(t, r.Header.Get("X-Request-ID"))
	return response{r.StatusCode, r.Header, data}
}

func write(t *testing.T, method, path string, value any) response {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return call(t, method, path, data, "application/json")
}

func read[T any](t *testing.T, r response, status int) T {
	t.Helper()
	require.Equal(t, status, r.status, string(r.body))
	require.Equal(t, "application/json", r.header.Get("Content-Type"))
	var out T
	require.NoError(t, json.Unmarshal(r.body, &out))
	return out
}

func assertError(t *testing.T, r response, status int, code string) {
	t.Helper()
	body := read[struct {
		Error struct{ Code, Message string }
	}](t, r, status)
	require.Equal(t, code, body.Error.Code)
	require.NotEmpty(t, body.Error.Message)
	require.NotContains(t, body.Error.Message, "SQL")
}

func payload(category string, price int64) map[string]any {
	return map[string]any{"sku": strings.ToUpper(uuid.NewString()), "name": "Running shoe", "description": "Lightweight trainer", "category": category, "price_cents": price}
}

func create(t *testing.T, category string, price int64) product {
	t.Helper()
	r := write(t, "POST", "/v1/products", payload(category, price))
	p := read[product](t, r, 201)
	t.Cleanup(func() {
		res := call(t, "DELETE", "/v1/products/"+p.ID, nil, "")
		require.Contains(t, []int{204, 404}, res.status)
	})
	return p
}

func TestUC01CreateRetrieveAndValidation(t *testing.T) {
	only(t, "normal")
	body := payload("create-case", 0)
	r := write(t, "POST", "/v1/products", body)
	p := read[product](t, r, 201)
	require.NoError(t, uuid.Validate(p.ID))
	require.Equal(t, "/v1/products/"+p.ID, r.header.Get("Location"))
	require.False(t, p.CreatedAt.IsZero())
	require.False(t, p.UpdatedAt.IsZero())
	require.Zero(t, p.Price)
	t.Cleanup(func() { call(t, "DELETE", "/v1/products/"+p.ID, nil, "") })
	got := read[product](t, call(t, "GET", r.header.Get("Location"), nil, ""), 200)
	require.Equal(t, p, got)
	for _, tc := range []struct {
		name, key string
		value     any
		omit      bool
	}{
		{"missing sku", "sku", nil, true}, {"null sku", "sku", nil, false}, {"lowercase sku", "sku", "bad", false},
		{"missing name", "name", nil, true}, {"blank name", "name", " \t", false}, {"long name", "name", strings.Repeat("é", 201), false},
		{"category", "category", "Not a slug", false}, {"long description", "description", strings.Repeat("x", 5001), false},
		{"missing price", "price_cents", nil, true}, {"null price", "price_cents", nil, false}, {"negative price", "price_cents", -1, false}, {"fractional price", "price_cents", 1.5, false}, {"large price", "price_cents", int64(1000000000001), false},
		{"unknown field", "extra", "value", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := payload("invalid-case", 100)
			if tc.omit {
				delete(in, tc.key)
			} else {
				in[tc.key] = tc.value
			}
			assertError(t, write(t, "POST", "/v1/products", in), 400, "validation_error")
		})
	}
	valid, err := json.Marshal(payload("invalid-case", 1))
	require.NoError(t, err)
	for _, body := range []string{"{", "null", "[]", string(valid) + " {}", string(valid) + " trailing"} {
		assertError(t, call(t, "POST", "/v1/products", []byte(body), "application/json"), 400, "validation_error")
	}
	assertError(t, call(t, "POST", "/v1/products", valid, "text/plain"), 415, "unsupported_media_type")
	assertError(t, call(t, "POST", "/v1/products", []byte(`{"name":"`+strings.Repeat("a", 65536)+`"}`), "application/json"), 413, "payload_too_large")
	invalid := read[page](t, call(t, "GET", "/v1/products?category=invalid-case", nil, ""), 200)
	require.Empty(t, invalid.Items)
}

func TestUC02UniqueSKU(t *testing.T) {
	only(t, "normal")
	one := create(t, "unique-case", 100)
	two := create(t, "unique-case", 200)
	in := payload(two.Category, 300)
	in["sku"] = one.SKU
	assertError(t, write(t, "POST", "/v1/products", in), 409, "conflict")
	assertError(t, write(t, "PUT", "/v1/products/"+two.ID, in), 409, "conflict")
	for _, p := range []product{one, two} {
		got := read[product](t, call(t, "GET", "/v1/products/"+p.ID, nil, ""), 200)
		require.Equal(t, p, got)
	}
}

func TestUC03ReplaceProduct(t *testing.T) {
	only(t, "normal")
	p := create(t, "before-update", 100)
	in := payload("after-update", 300)
	in["name"] = "Updated shoe"
	delete(in, "description")
	updated := read[product](t, write(t, "PUT", "/v1/products/"+p.ID, in), 200)
	require.Equal(t, p.ID, updated.ID)
	require.Equal(t, p.CreatedAt, updated.CreatedAt)
	require.True(t, updated.UpdatedAt.After(p.UpdatedAt))
	require.Empty(t, updated.Description)
	require.Equal(t, "Updated shoe", updated.Name)
	require.Equal(t, int64(300), updated.Price)
	require.Equal(t, in["sku"], updated.SKU)
	got := read[product](t, call(t, "GET", "/v1/products/"+p.ID, nil, ""), 200)
	require.Equal(t, updated, got)
	old := read[page](t, call(t, "GET", "/v1/products?category=before-update", nil, ""), 200)
	require.Empty(t, old.Items)
	newPage := read[page](t, call(t, "GET", "/v1/products?category=after-update", nil, ""), 200)
	require.Equal(t, []product{updated}, newPage.Items)
	assertError(t, write(t, "PUT", "/v1/products/"+p.ID, map[string]string{"name": "incomplete"}), 400, "validation_error")
	got = read[product](t, call(t, "GET", "/v1/products/"+p.ID, nil, ""), 200)
	require.Equal(t, updated, got)
}

func TestUC04DeleteProduct(t *testing.T) {
	only(t, "normal")
	p := create(t, "delete-case", 100)
	path := "/v1/products/" + p.ID
	r := call(t, "DELETE", path, nil, "")
	require.Equal(t, 204, r.status)
	require.Empty(t, r.body)
	assertError(t, call(t, "GET", path, nil, ""), 404, "not_found")
	assertError(t, call(t, "DELETE", path, nil, ""), 404, "not_found")
	listed := read[page](t, call(t, "GET", "/v1/products?category=delete-case", nil, ""), 200)
	require.Empty(t, listed.Items)
	require.NotNil(t, listed.Items)
	require.Nil(t, listed.Next)
}

func TestUC05BrowseFilterPaginate(t *testing.T) {
	only(t, "normal")
	var products []product
	for _, price := range []int64{0, 100, 100, 200, 300} {
		products = append(products, create(t, "browse-case", price))
	}
	create(t, "other-case", 150)
	for _, tc := range []struct {
		query string
		count int
	}{
		{"category=browse-case", 5}, {"category=browse-case&min_price_cents=100", 4}, {"category=browse-case&max_price_cents=100", 3}, {"category=browse-case&min_price_cents=0&max_price_cents=0", 1}, {"category=absent-case", 0},
	} {
		p := read[page](t, call(t, "GET", "/v1/products?"+tc.query, nil, ""), 200)
		require.Len(t, p.Items, tc.count)
		require.Nil(t, p.Next)
	}
	// Exercise global range predicates as well as category-constrained predicates.
	for _, query := range []string{"", "min_price_cents=100", "max_price_cents=200"} {
		p := read[page](t, call(t, "GET", "/v1/products?"+query, nil, ""), 200)
		require.NotEmpty(t, p.Items)
		for i, item := range p.Items {
			if query == "min_price_cents=100" {
				require.GreaterOrEqual(t, item.Price, int64(100))
			}
			if query == "max_price_cents=200" {
				require.LessOrEqual(t, item.Price, int64(200))
			}
			if i > 0 {
				prev := p.Items[i-1]
				require.True(t, prev.Price < item.Price || prev.Price == item.Price && prev.ID < item.ID)
			}
		}
	}
	filters := "category=browse-case&min_price_cents=100&max_price_cents=200"
	first := read[page](t, call(t, "GET", "/v1/products?"+filters+"&limit=2", nil, ""), 200)
	require.Len(t, first.Items, 2)
	require.NotNil(t, first.Next)
	second := read[page](t, call(t, "GET", "/v1/products?"+filters+"&limit=1&cursor="+url.QueryEscape(*first.Next), nil, ""), 200)
	require.Len(t, second.Items, 1)
	require.Nil(t, second.Next)
	want := append([]product(nil), products[1:4]...)
	sort.Slice(want, func(i, j int) bool {
		if want[i].Price != want[j].Price {
			return want[i].Price < want[j].Price
		}
		return want[i].ID < want[j].ID
	})
	require.Equal(t, want, append(first.Items, second.Items...))
	assertError(t, call(t, "GET", "/v1/products?category=other-case&cursor="+*first.Next, nil, ""), 400, "validation_error")
	for _, query := range []string{"limit=0", "limit=101", "limit=abc", "limit=1&limit=2", "unknown=1", "category=Bad", "min_price_cents=-1", "max_price_cents=1.5", "min_price_cents=2&max_price_cents=1", "cursor=invalid", "category="} {
		assertError(t, call(t, "GET", "/v1/products?"+query, nil, ""), 400, "validation_error")
	}
	for i := 0; i < 101; i++ {
		create(t, "page-limits", int64(i))
	}
	defaultPage := read[page](t, call(t, "GET", "/v1/products?category=page-limits", nil, ""), 200)
	require.Len(t, defaultPage.Items, 20)
	require.NotNil(t, defaultPage.Next)
	maxPage := read[page](t, call(t, "GET", "/v1/products?category=page-limits&limit=100", nil, ""), 200)
	require.Len(t, maxPage.Items, 100)
	require.NotNil(t, maxPage.Next)
}

func TestUC06MissingAndInvalidResources(t *testing.T) {
	only(t, "normal")
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		assertError(t, write(t, method, "/v1/products/"+uuid.NewString(), payload("missing-case", 1)), 404, "not_found")
		assertError(t, write(t, method, "/v1/products/bad-id", payload("missing-case", 1)), 400, "validation_error")
	}
	assertError(t, call(t, "GET", "/unknown-route", nil, ""), 404, "not_found")
	r := call(t, "PATCH", "/v1/products/"+uuid.NewString(), nil, "")
	assertError(t, r, 405, "method_not_allowed")
	require.Equal(t, "GET, PUT, DELETE", r.header.Get("Allow"))
}

func TestUC07Health(t *testing.T) {
	only(t, "normal")
	for _, path := range []string{"/healthz", "/readyz"} {
		r := call(t, "GET", path, nil, "")
		require.Equal(t, 200, r.status)
	}
	// Keep this row across the outage/recovery phases in the same Compose volume.
	p := read[product](t, write(t, "POST", "/v1/products", payload("outage-proof", 123)), 201)
	require.NotEmpty(t, p.ID)
}

func TestUC07Outage(t *testing.T) {
	only(t, "outage")
	r := call(t, "GET", "/healthz", nil, "")
	require.Equal(t, 200, r.status)
	assertError(t, call(t, "GET", "/readyz", nil, ""), 503, "unavailable")
	start := time.Now()
	assertError(t, call(t, "GET", "/v1/products", nil, ""), 503, "unavailable")
	require.Less(t, time.Since(start), 7*time.Second)
}

func TestUC07Recovery(t *testing.T) {
	only(t, "recovery")
	p := read[page](t, call(t, "GET", "/v1/products?category=outage-proof", nil, ""), 200)
	require.Len(t, p.Items, 1)
	require.Equal(t, int64(123), p.Items[0].Price)
	got := read[product](t, call(t, "GET", "/v1/products/"+p.Items[0].ID, nil, ""), 200)
	require.Equal(t, p.Items[0], got)
	require.Equal(t, 204, call(t, "DELETE", "/v1/products/"+got.ID, nil, "").status)
}
