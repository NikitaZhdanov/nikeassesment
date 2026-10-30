package catalog

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/google/uuid"
)

type Filters struct {
	Category string `json:"category"`
	Min      *int64 `json:"min"`
	Max      *int64 `json:"max"`
}

type Position struct {
	Price int64     `json:"price"`
	ID    uuid.UUID `json:"id"`
}

type Query struct {
	Filters
	Limit int
	After *Position
}

type cursor struct {
	Version int `json:"v"`
	Position
	Fingerprint string `json:"filters"`
}

func ParseQuery(raw string) (Query, error) {
	q := Query{Limit: 20}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, errors.New("invalid query encoding")
	}
	for key, values := range values {
		if len(values) != 1 || values[0] == "" {
			return q, errors.New("query parameters must have one nonempty value")
		}
		switch key {
		case "category", "min_price_cents", "max_price_cents", "limit", "cursor":
		default:
			return q, errors.New("unknown query parameter")
		}
	}
	q.Category = values.Get("category")
	if q.Category != "" {
		if err := ValidateCategory(q.Category); err != nil {
			return q, err
		}
	}
	for key, dest := range map[string]**int64{"min_price_cents": &q.Min, "max_price_cents": &q.Max} {
		if value := values.Get(key); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || n > MaxPrice {
				return q, errors.New("price bounds must be integers within the supported range")
			}
			*dest = &n
		}
	}
	if q.Min != nil && q.Max != nil && *q.Min > *q.Max {
		return q, errors.New("minimum price exceeds maximum price")
	}
	if value := values.Get("limit"); value != "" {
		q.Limit, err = strconv.Atoi(value)
		if err != nil || q.Limit < 1 || q.Limit > 100 {
			return q, errors.New("limit must be an integer from 1 to 100")
		}
	}
	if value := values.Get("cursor"); value != "" {
		q.After, err = decodeCursor(value, q.Filters)
	}
	return q, err
}

func fingerprint(f Filters) string {
	data, _ := json.Marshal(f) // Filters contains only JSON-safe scalar types.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func EncodeCursor(p Product, f Filters) string {
	data, _ := json.Marshal(cursor{Version: 1, Position: Position{Price: p.PriceCents, ID: p.ID}, Fingerprint: fingerprint(f)})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string, f Filters) (*Position, error) {
	invalid := errors.New("invalid cursor or cursor filters do not match")
	if len(value) > 1024 {
		return nil, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, invalid
	}
	var c cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, invalid
	}
	if c.Version != 1 || c.ID == uuid.Nil || c.Price < 0 || c.Price > MaxPrice || c.Fingerprint != fingerprint(f) {
		return nil, invalid
	}
	return &c.Position, nil
}

func NewPage(products []Product, q Query) Page {
	if products == nil {
		products = []Product{}
	}
	page := Page{Items: products}
	if len(products) > q.Limit {
		page.Items = products[:q.Limit]
		next := EncodeCursor(page.Items[len(page.Items)-1], q.Filters)
		page.NextCursor = &next
	}
	return page
}
