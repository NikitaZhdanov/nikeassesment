package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nikitazhdanov/nikeassesment/internal/catalog"
	"github.com/stretchr/testify/require"
)

type stubRepo struct {
	product    catalog.Product
	products   []catalog.Product
	err        error
	panicValue bool
	calls      int
	id         uuid.UUID
	input      catalog.Input
	query      catalog.Query
	deadline   bool
}

func (s *stubRepo) call(ctx context.Context) {
	s.calls++
	_, s.deadline = ctx.Deadline()
	if s.panicValue {
		panic("test panic")
	}
}
func (s *stubRepo) Create(ctx context.Context, in catalog.Input) (catalog.Product, error) {
	s.call(ctx)
	s.input = in
	return s.product, s.err
}
func (s *stubRepo) Get(ctx context.Context, id uuid.UUID) (catalog.Product, error) {
	s.call(ctx)
	s.id = id
	return s.product, s.err
}
func (s *stubRepo) Update(ctx context.Context, id uuid.UUID, in catalog.Input) (catalog.Product, error) {
	s.call(ctx)
	s.id = id
	s.input = in
	return s.product, s.err
}
func (s *stubRepo) Delete(ctx context.Context, id uuid.UUID) error {
	s.call(ctx)
	s.id = id
	return s.err
}
func (s *stubRepo) List(ctx context.Context, q catalog.Query) ([]catalog.Product, error) {
	s.call(ctx)
	s.query = q
	return s.products, s.err
}

const validJSON = `{"sku":"SHOE-01","name":"Shoe","description":"A shoe","category":"shoes","price_cents":100}`

func handler(s *stubRepo, pingErr error) http.Handler {
	return New(s, func(context.Context) error { return pingErr }, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func request(h http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
	var body struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, code, body.Error.Code)
	require.NotEmpty(t, body.Error.Message)
}

func TestProductSuccess(t *testing.T) {
	p := catalog.Product{ID: uuid.New(), SKU: "SHOE-01", Name: "Shoe", Category: "shoes", PriceCents: 100, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/v1/products", 201}, {"GET", "/v1/products/" + p.ID.String(), 200}, {"PUT", "/v1/products/" + p.ID.String(), 200}, {"DELETE", "/v1/products/" + p.ID.String(), 204},
	} {
		t.Run(tc.method, func(t *testing.T) {
			s := &stubRepo{product: p}
			w := request(handler(s, nil), tc.method, tc.path, validJSON, "application/json; charset=utf-8")
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, 1, s.calls)
			require.True(t, s.deadline)
			require.NotEmpty(t, w.Header().Get("X-Request-ID"))
			if tc.method == "DELETE" {
				require.Empty(t, w.Body.String())
			} else {
				var got catalog.Product
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
				require.Equal(t, p, got)
			}
			if tc.method == "POST" {
				require.Equal(t, "/v1/products/"+p.ID.String(), w.Header().Get("Location"))
			} else {
				require.Equal(t, p.ID, s.id)
			}
			if tc.method == "POST" || tc.method == "PUT" {
				require.Equal(t, "SHOE-01", s.input.SKU)
				require.Equal(t, int64(100), *s.input.PriceCents)
			}
		})
	}
}

func TestInvalidWrites(t *testing.T) {
	for _, tc := range []struct {
		name, body, media string
		status            int
		code              string
	}{
		{"malformed", "{", "application/json", 400, "validation_error"},
		{"empty", "", "application/json", 400, "validation_error"},
		{"null", "null", "application/json", 400, "validation_error"},
		{"array", "[]", "application/json", 400, "validation_error"},
		{"unknown", strings.Replace(validJSON, `"sku"`, `"unknown"`, 1), "application/json", 400, "validation_error"},
		{"multiple", validJSON + ` {}`, "application/json", 400, "validation_error"},
		{"trailing junk", validJSON + ` x`, "application/json", 400, "validation_error"},
		{"missing", "{}", "application/json", 400, "validation_error"},
		{"null price", strings.Replace(validJSON, `100`, `null`, 1), "application/json", 400, "validation_error"},
		{"fractional", strings.Replace(validJSON, `100`, `1.2`, 1), "application/json", 400, "validation_error"},
		{"wrong media", validJSON, "text/plain", 415, "unsupported_media_type"},
		{"missing media", validJSON, "", 415, "unsupported_media_type"},
		{"broken media", validJSON, "application/json; charset", 415, "unsupported_media_type"},
		{"oversized", `{"name":"` + strings.Repeat("a", 65536) + `"}`, "application/json", 413, "payload_too_large"},
		{"oversized trailing whitespace", validJSON + strings.Repeat(" ", 65536), "application/json", 413, "payload_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{"POST", "PUT"} {
				path := "/v1/products"
				if method == "PUT" {
					path += "/" + uuid.NewString()
				}
				s := &stubRepo{}
				w := request(handler(s, nil), method, path, tc.body, tc.media)
				assertError(t, w, tc.status, tc.code)
				require.Zero(t, s.calls)
			}
		})
	}
}

func TestErrorsAndBadIDs(t *testing.T) {
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		s := &stubRepo{}
		w := request(handler(s, nil), method, "/v1/products/not-uuid", validJSON, "application/json")
		assertError(t, w, 400, "validation_error")
		require.Zero(t, s.calls)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{catalog.ErrNotFound, 404, "not_found"}, {catalog.ErrConflict, 409, "conflict"}, {errors.New("secret SQL password"), 503, "unavailable"},
	} {
		for _, method := range []string{"POST", "GET", "PUT", "DELETE"} {
			path := "/v1/products"
			if method != "POST" {
				path += "/" + uuid.NewString()
			}
			w := request(handler(&stubRepo{err: tc.err}, nil), method, path, validJSON, "application/json")
			assertError(t, w, tc.status, tc.code)
			require.NotContains(t, w.Body.String(), "secret")
		}
	}
}

func TestList(t *testing.T) {
	p := catalog.Product{ID: uuid.New(), PriceCents: 10}
	s := &stubRepo{products: []catalog.Product{p, {ID: uuid.New(), PriceCents: 20}}}
	w := request(handler(s, nil), "GET", "/v1/products?category=shoes&min_price_cents=0&limit=1", "", "")
	require.Equal(t, 200, w.Code)
	var page catalog.Page
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.NextCursor)
	require.Equal(t, "shoes", s.query.Category)
	require.Equal(t, 1, s.query.Limit)
	w = request(handler(&stubRepo{}, nil), "GET", "/v1/products", "", "")
	require.JSONEq(t, `{"items":[],"next_cursor":null}`, w.Body.String())
	w = request(handler(&stubRepo{}, nil), "GET", "/v1/products?limit=0", "", "")
	assertError(t, w, 400, "validation_error")
	w = request(handler(&stubRepo{err: errors.New("offline")}, nil), "GET", "/v1/products", "", "")
	assertError(t, w, 503, "unavailable")
}

func TestHealthRoutingRecovery(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		w := request(handler(&stubRepo{}, nil), "GET", path, "", "")
		require.Equal(t, 200, w.Code)
	}
	w := request(handler(&stubRepo{}, errors.New("offline")), "GET", "/readyz", "", "")
	assertError(t, w, 503, "unavailable")
	w = request(handler(&stubRepo{}, nil), "GET", "/missing", "", "")
	assertError(t, w, 404, "not_found")
	w = request(handler(&stubRepo{}, nil), "PATCH", "/v1/products/"+uuid.NewString(), "", "")
	assertError(t, w, 405, "method_not_allowed")
	require.Equal(t, "GET, PUT, DELETE", w.Header().Get("Allow"))
	for path, allowed := range map[string]string{"/v1/products": "GET, POST", "/healthz": "GET", "/readyz": "GET"} {
		w = request(handler(&stubRepo{}, nil), "PATCH", path, "", "")
		assertError(t, w, 405, "method_not_allowed")
		require.Equal(t, allowed, w.Header().Get("Allow"))
	}
	w = request(handler(&stubRepo{panicValue: true}, nil), "GET", "/v1/products", "", "")
	assertError(t, w, 500, "internal_error")
	require.NotContains(t, w.Body.String(), "test panic")
}
