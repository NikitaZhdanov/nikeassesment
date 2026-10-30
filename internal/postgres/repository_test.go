package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nikitazhdanov/nikeassesment/internal/catalog"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/require"
)

func fixture() catalog.Product {
	return catalog.Product{ID: uuid.New(), SKU: "SHOE-01", Name: "Shoe", Description: "A shoe", Category: "shoes", PriceCents: 100, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func row(p catalog.Product) *pgxmock.Rows {
	return pgxmock.NewRows(strings.Split(columns, ", ")).AddRow(p.ID, p.SKU, p.Name, p.Description, p.Category, p.PriceCents, p.CreatedAt, p.UpdatedAt)
}

func input(p catalog.Product) catalog.Input {
	return catalog.Input{SKU: p.SKU, Name: p.Name, Description: p.Description, Category: p.Category, PriceCents: &p.PriceCents}
}

func setup(t *testing.T) (*Repository, pgxmock.PgxPoolIface) {
	t.Helper()
	db, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.ExpectationsWereMet()); db.Close() })
	return New(db), db
}

func TestCRUD(t *testing.T) {
	r, db := setup(t)
	p := fixture()
	ctx := context.Background()
	db.ExpectQuery("INSERT INTO products").WithArgs(pgxmock.AnyArg(), p.SKU, p.Name, p.Description, p.Category, p.PriceCents).WillReturnRows(row(p))
	got, err := r.Create(ctx, input(p))
	require.NoError(t, err)
	require.Equal(t, p, got)
	db.ExpectQuery("SELECT .* WHERE id = \\$1").WithArgs(p.ID.String()).WillReturnRows(row(p))
	got, err = r.Get(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, p, got)
	db.ExpectQuery("UPDATE products SET").WithArgs(p.ID.String(), p.SKU, p.Name, p.Description, p.Category, p.PriceCents).WillReturnRows(row(p))
	got, err = r.Update(ctx, p.ID, input(p))
	require.NoError(t, err)
	require.Equal(t, p, got)
	db.ExpectExec("DELETE FROM products").WithArgs(p.ID.String()).WillReturnResult(pgxmock.NewResult("DELETE", 1))
	require.NoError(t, r.Delete(ctx, p.ID))
}

func TestRepositoryErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		dbErr, want error
	}{
		{"missing", pgx.ErrNoRows, catalog.ErrNotFound},
		{"conflict", &pgconn.PgError{Code: "23505"}, catalog.ErrConflict},
		{"database", errors.New("offline"), nil},
		{"constraint", &pgconn.PgError{Code: "23514"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := setup(t)
			p := fixture()
			want := tc.want
			if want == nil {
				want = tc.dbErr
			}
			db.ExpectQuery("SELECT").WithArgs(p.ID.String()).WillReturnError(tc.dbErr)
			_, err := r.Get(context.Background(), p.ID)
			require.ErrorIs(t, err, want)
			db.ExpectQuery("INSERT").WithArgs(pgxmock.AnyArg(), p.SKU, p.Name, p.Description, p.Category, p.PriceCents).WillReturnError(tc.dbErr)
			_, err = r.Create(context.Background(), input(p))
			require.ErrorIs(t, err, want)
			db.ExpectQuery("UPDATE").WithArgs(p.ID.String(), p.SKU, p.Name, p.Description, p.Category, p.PriceCents).WillReturnError(tc.dbErr)
			_, err = r.Update(context.Background(), p.ID, input(p))
			require.ErrorIs(t, err, want)
		})
	}
	r, db := setup(t)
	db.ExpectExec("DELETE").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
	require.ErrorIs(t, r.Delete(context.Background(), uuid.New()), catalog.ErrNotFound)
	db.ExpectExec("DELETE").WithArgs(pgxmock.AnyArg()).WillReturnError(context.Canceled)
	require.ErrorIs(t, r.Delete(context.Background(), uuid.New()), context.Canceled)
}

func TestListQueryAndResults(t *testing.T) {
	r, db := setup(t)
	p := fixture()
	min, max := int64(0), int64(1000)
	q := catalog.Query{Filters: catalog.Filters{Category: "shoes", Min: &min, Max: &max}, Limit: 2, After: &catalog.Position{Price: 99, ID: p.ID}}
	expected := "SELECT " + columns + " FROM products WHERE category = $1 AND price_cents >= $2 AND price_cents <= $3 AND (price_cents, id) > ($4, $5) ORDER BY price_cents, id LIMIT $6"
	db.ExpectQuery(regexp.QuoteMeta(expected)).WithArgs("shoes", min, max, int64(99), p.ID.String(), 3).WillReturnRows(row(p)).RowsWillBeClosed()
	products, err := r.List(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, []catalog.Product{p}, products)
	db.ExpectQuery(regexp.QuoteMeta("SELECT " + columns + " FROM products ORDER BY price_cents, id LIMIT $1")).WithArgs(21).WillReturnRows(pgxmock.NewRows(strings.Split(columns, ", "))).RowsWillBeClosed()
	products, err = r.List(context.Background(), catalog.Query{Limit: 20})
	require.NoError(t, err)
	require.Empty(t, products)
}

func TestListErrors(t *testing.T) {
	r, db := setup(t)
	ctx := context.Background()
	q := catalog.Query{Limit: 2}
	db.ExpectQuery("SELECT").WithArgs(3).WillReturnError(context.DeadlineExceeded)
	_, err := r.List(ctx, q)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	db.ExpectQuery("SELECT").WithArgs(3).WillReturnRows(pgxmock.NewRows([]string{"bad"}).AddRow("not a product")).RowsWillBeClosed()
	_, err = r.List(ctx, q)
	require.Error(t, err)
	db.ExpectQuery("SELECT").WithArgs(3).WillReturnRows(row(fixture()).RowError(0, context.Canceled)).RowsWillBeClosed()
	_, err = r.List(ctx, q)
	require.ErrorIs(t, err, context.Canceled)
}
