package catalog

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestParseQuery(t *testing.T) {
	q, err := ParseQuery("")
	require.NoError(t, err)
	require.Equal(t, 20, q.Limit)
	require.Nil(t, q.Min)
	q, err = ParseQuery("category=shoes&min_price_cents=0&max_price_cents=1000000000000&limit=100")
	require.NoError(t, err)
	require.Equal(t, "shoes", q.Category)
	require.Equal(t, int64(0), *q.Min)
	require.Equal(t, MaxPrice, *q.Max)
	require.Equal(t, 100, q.Limit)
	for _, raw := range []string{"%zz", "a=b;c", "unknown=1", "limit=1&limit=2", "limit=", "category=Bad", "min_price_cents=-1", "max_price_cents=1000000000001", "min_price_cents=1.5", "max_price_cents=9223372036854775808", "min_price_cents=2&max_price_cents=1", "limit=0", "limit=101", "limit=no", "cursor=%%%"} {
		t.Run(raw, func(t *testing.T) { _, err := ParseQuery(raw); require.Error(t, err) })
	}
}

func TestCursorRoundTripAndFilterBinding(t *testing.T) {
	f := Filters{Category: "shoes", Min: ptr(0), Max: ptr(200)}
	p := Product{ID: uuid.New(), PriceCents: 100}
	token := EncodeCursor(p, f)
	q, err := ParseQuery("category=shoes&min_price_cents=0&max_price_cents=200&limit=1&cursor=" + url.QueryEscape(token))
	require.NoError(t, err)
	require.Equal(t, &Position{Price: 100, ID: p.ID}, q.After)
	for _, changed := range []Filters{{}, {Category: "shirts", Min: f.Min, Max: f.Max}, {Category: f.Category, Max: f.Max}, {Category: f.Category, Min: f.Min, Max: ptr(201)}} {
		_, err := decodeCursor(token, changed)
		require.Error(t, err)
	}
}

func TestInvalidCursors(t *testing.T) {
	f := Filters{}
	encode := func(c cursor) string {
		b, err := json.Marshal(c)
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	good := cursor{Version: 1, Position: Position{ID: uuid.New(), Price: 1}, Fingerprint: fingerprint(f)}
	tokens := []string{strings.Repeat("a", 1025), "%%%", base64.RawURLEncoding.EncodeToString([]byte("{")), base64.RawURLEncoding.EncodeToString([]byte(`{"id":"bad"}`))}
	for _, change := range []func(*cursor){func(c *cursor) { c.Version = 2 }, func(c *cursor) { c.ID = uuid.Nil }, func(c *cursor) { c.Price = -1 }, func(c *cursor) { c.Price = MaxPrice + 1 }, func(c *cursor) { c.Fingerprint = "bad" }} {
		c := good
		change(&c)
		tokens = append(tokens, encode(c))
	}
	for _, token := range tokens {
		_, err := decodeCursor(token, f)
		require.Error(t, err)
	}
}

func TestNewPage(t *testing.T) {
	q := Query{Limit: 2}
	page := NewPage(nil, q)
	require.NotNil(t, page.Items)
	require.Empty(t, page.Items)
	require.Nil(t, page.NextCursor)
	products := []Product{{ID: uuid.New(), PriceCents: 10}, {ID: uuid.New(), PriceCents: 10}, {ID: uuid.New(), PriceCents: 11}}
	page = NewPage(products[:2], q)
	require.Len(t, page.Items, 2)
	require.Nil(t, page.NextCursor)
	page = NewPage(products, q)
	require.Len(t, page.Items, 2)
	require.NotNil(t, page.NextCursor)
	position, err := decodeCursor(*page.NextCursor, q.Filters)
	require.NoError(t, err)
	require.Equal(t, products[1].ID, position.ID)
}
