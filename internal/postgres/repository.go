package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
		return classify(err)
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
		return nil, classify(err)
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
	return products, classify(rows.Err())
}

func scan(row pgx.Row) (catalog.Product, error) {
	var p catalog.Product
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.Category, &p.PriceCents, &p.CreatedAt, &p.UpdatedAt)
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, classify(err)
}

// Keep driver details inside this package while retaining the original cause
// for structured logs. Unknown SQL/driver errors remain internal failures.
func classify(err error) error {
	if err == nil {
		return nil
	}
	// A column conversion failure is internal even when a codec wraps EOF or
	// a timeout. Those causes indicate unavailability only on the transport path.
	var scanErr pgx.ScanArgError
	if errors.As(err, &scanErr) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", catalog.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505" && pgErr.ConstraintName == "products_sku_key":
			return fmt.Errorf("%w: %w", catalog.ErrConflict, err)
		case pgErr.Code == "23514" && pgErr.TableName == "products":
			switch pgErr.ConstraintName {
			case "products_sku_check", "products_name_check", "products_description_check", "products_category_check", "products_price_cents_check":
				return fmt.Errorf("%w: %w", catalog.ErrInvalidInput, err)
			}
		case pgErr.Code == "08000", pgErr.Code == "08001", pgErr.Code == "08003", pgErr.Code == "08004", pgErr.Code == "08006", pgErr.Code == "08007",
			pgErr.Code == "53300", pgErr.Code == "57P01", pgErr.Code == "57P02", pgErr.Code == "57P03",
			pgErr.Code == "57014", pgErr.Code == "55P03":
			return fmt.Errorf("%w: %w", catalog.ErrUnavailable, err)
		}
		// In particular, protocol, authentication, syntax, schema and unexpected
		// constraint errors are not evidence of temporary unavailability.
		return err
	}
	var connectErr *pgconn.ConnectError
	var netErr net.Error
	if errors.As(err, &connectErr) || errors.As(err, &netErr) || pgconn.Timeout(err) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("%w: %w", catalog.ErrUnavailable, err)
	}
	return err
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
