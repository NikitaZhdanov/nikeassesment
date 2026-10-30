CREATE TABLE products (
    id UUID PRIMARY KEY,
    sku TEXT NOT NULL UNIQUE CHECK (sku ~ '^[A-Z0-9][A-Z0-9_-]{0,63}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200 AND name ~ '[^[:space:]]'),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 5000),
    category TEXT NOT NULL CHECK (char_length(category) BETWEEN 1 AND 64 AND category ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    price_cents BIGINT NOT NULL CHECK (price_cents BETWEEN 0 AND 1000000000000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX products_price_id_idx ON products (price_cents, id);
CREATE INDEX products_category_price_id_idx ON products (category, price_cents, id);
