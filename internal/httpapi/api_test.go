package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestStrictProductJSON(t *testing.T) {
	type testCase struct{ name, body string }
	tests := []testCase{
		{"scalar", `true`},
		{"UTF-8 in property", strings.Replace(validJSON, `"name"`, "\"na\xffme\"", 1)},
		{"UTF-8 after object", validJSON + "\xff"},
		{"UTF-8 before object", "\xff" + validJSON},
		{"missing colon", `{"sku" "SHOE-01"}`},
		{"missing closing brace", strings.TrimSuffix(validJSON, "}")},
		{"trailing comma", strings.TrimSuffix(validJSON, "}") + ",}"},
		{"wrong closing delimiter", strings.TrimSuffix(validJSON, "}") + "]"},
		{"wrong value type", strings.Replace(validJSON, `"Shoe"`, `{}`, 1)},
		{"nested unknown object", strings.TrimSuffix(validJSON, "}") + `,"extra":{"name":"value"}}`},
		{"trailing null", validJSON + ` null`},
		{"trailing array", validJSON + ` []`},
		{"escaped duplicate", strings.TrimSuffix(validJSON, "}") + `,"\u0073ku":"SHOE-02"}`},
	}
	for _, field := range []string{"sku", "name", "description", "category", "price_cents"} {
		key := `"` + field + `"`
		for _, variant := range []string{strings.ToUpper(field), strings.ToUpper(field[:1]) + field[1:]} {
			tests = append(tests, testCase{"case " + variant, strings.Replace(validJSON, key, `"`+variant+`"`, 1)})
			for _, prefix := range []bool{false, true} {
				member := `"` + variant + `":null,`
				body := "{" + member + validJSON[1:]
				if !prefix {
					body = strings.TrimSuffix(validJSON, "}") + "," + strings.TrimSuffix(member, ",") + "}"
				}
				tests = append(tests, testCase{fmt.Sprintf("mixed %s prefix=%t", variant, prefix), body})
			}
		}
		for _, value := range []string{`null`, `"different"`} {
			tests = append(tests, testCase{"duplicate " + field + " " + value, strings.TrimSuffix(validJSON, "}") + "," + key + ":" + value + "}"})
		}
		// Include identical repeated members as well as conflicting duplicates.
		var members map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(validJSON), &members))
		tests = append(tests, testCase{"identical duplicate " + field, "{" + key + ":" + string(members[field]) + "," + validJSON[1:]})
		if field != "description" {
			for _, missing := range []bool{false, true} {
				members[field] = json.RawMessage("null")
				if missing {
					delete(members, field)
				}
				body, err := json.Marshal(members)
				require.NoError(t, err)
				tests = append(tests, testCase{fmt.Sprintf("required %s missing=%t", field, missing), string(body)})
			}
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{"POST", "PUT"} {
				s := &stubRepo{}
				path := "/v1/products"
				if method == "PUT" {
					path += "/" + uuid.NewString()
				}
				assertError(t, request(handler(s, nil), method, path, tc.body, "application/json"), 400, "validation_error")
				require.Zero(t, s.calls)
			}
		})
	}
}

func TestProductEncoding(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"multibyte", "  Café 靴 👟\t ", true},
		{"replacement character", "\ufffd", true},
		{"escaped Unicode", `\u00e9\u9774\ud83d\udc5f`, true},
		{"escaped backslash is not NUL", `\\u0000`, true},
		{"invalid byte", "\xff", false},
		{"continuation byte", "\x80", false},
		{"truncated sequence", "\xe2\x82", false},
		{"overlong sequence", "\xc0\xaf", false},
		{"UTF-8 surrogate", "\xed\xa0\x80", false},
		{"above Unicode range", "\xf4\x90\x80\x80", false},
		{"raw NUL", "\x00", false},
		{"escaped NUL", `\u0000`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, field := range []string{"name", "description"} {
				for _, method := range []string{"POST", "PUT"} {
					// Raw bytes are intentional: json.Marshal would repair malformed UTF-8.
					value := tc.value
					if tc.name == "multibyte" {
						encoded, err := json.Marshal(value)
						require.NoError(t, err)
						value = string(encoded[1 : len(encoded)-1])
					}
					old := `"` + field + `":"` + map[string]string{"name": "Shoe", "description": "A shoe"}[field] + `"`
					body := strings.Replace(validJSON, old, `"`+field+`":"`+value+`"`, 1)
					s := &stubRepo{}
					path, status := "/v1/products", 201
					if method == "PUT" {
						path, status = path+"/"+uuid.NewString(), 200
					}
					w := request(handler(s, nil), method, path, body, "application/json")
					if tc.valid {
						require.Equal(t, status, w.Code, w.Body.String())
						var expected catalog.Input
						require.NoError(t, json.Unmarshal([]byte(body), &expected))
						require.Equal(t, expected, s.input)
						if field == "name" && tc.name == "multibyte" {
							require.Equal(t, tc.value, s.input.Name)
						}
					} else {
						assertError(t, w, 400, "validation_error")
						require.Zero(t, s.calls)
					}
				}
			}
		})
	}
	for _, field := range []string{"sku", "name", "description", "category"} {
		t.Run("NUL in "+field, func(t *testing.T) {
			var values map[string]any
			require.NoError(t, json.Unmarshal([]byte(validJSON), &values))
			values[field] = "before\x00after"
			body, err := json.Marshal(values)
			require.NoError(t, err)
			for _, method := range []string{"POST", "PUT"} {
				s := &stubRepo{}
				path := "/v1/products"
				if method == "PUT" {
					path += "/" + uuid.NewString()
				}
				assertError(t, request(handler(s, nil), method, path, string(body), "application/json"), 400, "validation_error")
				require.Zero(t, s.calls)
			}
		})
	}
}

func TestValidWriteBoundaries(t *testing.T) {
	for _, body := range []string{
		validJSON + strings.Repeat(" ", (64<<10)-len(validJSON)),
		strings.Replace(validJSON, `"Shoe"`, `"`+strings.Repeat("👟", 200)+`"`, 1),
		strings.Replace(validJSON, `"A shoe"`, `"`+strings.Repeat("靴", 5000)+`"`, 1),
		strings.Replace(validJSON, `"description":"A shoe",`, "", 1),
		strings.Replace(validJSON, `"A shoe"`, `null`, 1),
		strings.Replace(validJSON, `"sku"`, `"\u0073ku"`, 1),
		strings.Replace(validJSON, `100`, `0`, 1),
	} {
		s := &stubRepo{}
		w := request(handler(s, nil), "POST", "/v1/products", body, "application/json")
		require.Equal(t, 201, w.Code, w.Body.String())
		require.Equal(t, 1, s.calls)
	}
	for _, body := range []string{
		validJSON + strings.Repeat(" ", (64<<10)-len(validJSON)+1),
		"{" + strings.Repeat(" ", 64<<10),
		validJSON + strings.Repeat(" ", (64<<10)-len(validJSON)) + "\xff",
	} {
		s := &stubRepo{}
		assertError(t, request(handler(s, nil), "POST", "/v1/products", body, "application/json"), 413, "payload_too_large")
		require.Zero(t, s.calls)
	}
}

func TestFailureLogging(t *testing.T) {
	for _, cause := range []error{errors.New("SQL bug: internal detail"), fmt.Errorf("%w: connection failed", catalog.ErrUnavailable)} {
		var logs bytes.Buffer
		h := New(&stubRepo{err: cause}, nil, slog.New(slog.NewJSONHandler(&logs, nil)))
		r := httptest.NewRequest("GET", "/v1/products", nil)
		r.Header.Set("X-Request-ID", "review-request-123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, "review-request-123", w.Header().Get("X-Request-ID"))
		var entry map[string]any
		require.NoError(t, json.NewDecoder(&logs).Decode(&entry))
		require.Equal(t, "ERROR", entry["level"])
		require.Equal(t, "review-request-123", entry["request_id"])
		require.Equal(t, cause.Error(), entry["error"])
		require.NotContains(t, w.Body.String(), cause.Error())
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
		{catalog.ErrNotFound, 404, "not_found"},
		{catalog.ErrConflict, 409, "conflict"},
		{errors.New("secret SQL password"), 500, "internal_error"},
		{fmt.Errorf("secret database URL: %w", catalog.ErrUnavailable), 503, "unavailable"},
		{fmt.Errorf("secret constraint: %w", catalog.ErrInvalidInput), 400, "validation_error"},
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
		w := request(handler(&stubRepo{err: tc.err}, nil), "GET", "/v1/products", "", "")
		assertError(t, w, tc.status, tc.code)
		require.NotContains(t, w.Body.String(), "secret")
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
	w = request(handler(&stubRepo{err: catalog.ErrUnavailable}, nil), "GET", "/v1/products", "", "")
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
