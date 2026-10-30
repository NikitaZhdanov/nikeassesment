package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nikitazhdanov/nikeassesment/internal/catalog"
)

type DB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type Repository struct{ db DB }

func New(db DB) *Repository { return &Repository{db: db} }

const columns = "id, sku, name, description, category, price_cents, created_at, updated_at"

func (r *Repository) Create(ctx context.Context, in catalog.Input) (catalog.Product, error) {
	return scan(r.db.QueryRow(ctx, `INSERT INTO products (id, sku, name, description, category, price_cents)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+columns,
		uuid.New().String(), in.SKU, in.Name, in.Description, in.Category, *in.PriceCents))
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (catalog.Product, error) {
	return scan(r.db.QueryRow(ctx, "SELECT "+columns+" FROM products WHERE id = $1", id.String()))
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, in catalog.Input) (catalog.Product, error) {
	return scan(r.db.QueryRow(ctx, `UPDATE products SET sku = $2, name = $3, description = $4,
		category = $5, price_cents = $6, updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
		WHERE id = $1 RETURNING `+columns,
		id.String(), in.SKU, in.Name, in.Description, in.Category, *in.PriceCents))
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, "DELETE FROM products WHERE id = $1", id.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return catalog.ErrNotFound
	}
	return nil
}

func (r *Repository) List(ctx context.Context, q catalog.Query) ([]catalog.Product, error) {
	sql, args := listSQL(q)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	products := make([]catalog.Product, 0, q.Limit+1)
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

func scan(row pgx.Row) (catalog.Product, error) {
	var p catalog.Product
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.Category, &p.PriceCents, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, catalog.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return p, catalog.ErrConflict
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

func listSQL(q catalog.Query) (string, []any) {
	var conditions []string
	var args []any
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if q.Category != "" {
		add("category = $%d", q.Category)
	}
	if q.Min != nil {
		add("price_cents >= $%d", *q.Min)
	}
	if q.Max != nil {
		add("price_cents <= $%d", *q.Max)
	}
	if q.After != nil {
		args = append(args, q.After.Price, q.After.ID.String())
		conditions = append(conditions, fmt.Sprintf("(price_cents, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	sql := "SELECT " + columns + " FROM products"
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, q.Limit+1)
	sql += fmt.Sprintf(" ORDER BY price_cents, id LIMIT $%d", len(args))
	return sql, args
}
