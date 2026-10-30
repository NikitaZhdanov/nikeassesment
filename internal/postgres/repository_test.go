package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"
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
		{"conflict", &pgconn.PgError{Code: "23505", ConstraintName: "products_sku_key"}, catalog.ErrConflict},
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

func TestErrorClassification(t *testing.T) {
	config, err := pgconn.ParseConfig("postgres://test@localhost/test?sslmode=disable")
	require.NoError(t, err)
	dialCause := errors.New("connection attempt failed")
	config.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		return nil, dialCause
	}
	_, connectErr := pgconn.ConnectConfig(context.Background(), config)
	require.Error(t, connectErr)
	dialCause = &pgconn.PgError{Code: "28P01"}
	_, authErr := pgconn.ConnectConfig(context.Background(), config)
	require.Error(t, authErr)
	type testCase struct {
		name        string
		cause, want error
	}
	tests := []testCase{
		{"no rows", pgx.ErrNoRows, catalog.ErrNotFound},
		{"SKU conflict", &pgconn.PgError{Code: "23505", ConstraintName: "products_sku_key"}, catalog.ErrConflict},
		{"primary key conflict", &pgconn.PgError{Code: "23505", ConstraintName: "products_pkey"}, nil},
		{"unnamed unique violation", &pgconn.PgError{Code: "23505"}, nil},
		{"other check", &pgconn.PgError{Code: "23514", TableName: "products", ConstraintName: "unknown_check"}, nil},
		{"other table", &pgconn.PgError{Code: "23514", TableName: "other", ConstraintName: "products_name_check"}, nil},
		{"NOT NULL bug", &pgconn.PgError{Code: "23502", ColumnName: "id"}, nil},
		{"syntax bug", &pgconn.PgError{Code: "42601", Message: "secret SQL"}, nil},
		{"undefined table", &pgconn.PgError{Code: "42P01"}, nil},
		{"undefined column", &pgconn.PgError{Code: "42703"}, nil},
		{"protocol bug", &pgconn.PgError{Code: "08P01"}, nil},
		{"bad credentials", &pgconn.PgError{Code: "28P01"}, nil},
		{"authentication in connection wrapper", authErr, nil},
		{"bad parameter", &pgconn.PgError{Code: "22P02"}, nil},
		{"driver bug", errors.New("scan conversion failure"), nil},
		{"scan EOF is not a connection loss", pgx.ScanArgError{ColumnIndex: 0, Err: io.EOF}, nil},
		{"scan timeout is not an outage", pgx.ScanArgError{ColumnIndex: 0, Err: context.DeadlineExceeded}, nil},
		{"closed transaction", pgx.ErrTxClosed, nil},
		{"connect failure", connectErr, catalog.ErrUnavailable},
		{"connection reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, catalog.ErrUnavailable},
		{"connection refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, catalog.ErrUnavailable},
		{"DNS failure", &net.DNSError{Err: "no such host", Name: "db"}, catalog.ErrUnavailable},
		{"network timeout", &net.DNSError{Err: "timeout", IsTimeout: true}, catalog.ErrUnavailable},
		{"deadline", context.DeadlineExceeded, catalog.ErrUnavailable},
		{"canceled operation", context.Canceled, catalog.ErrUnavailable},
		{"EOF", io.EOF, catalog.ErrUnavailable},
		{"unexpected EOF", io.ErrUnexpectedEOF, catalog.ErrUnavailable},
		{"closed connection", net.ErrClosed, catalog.ErrUnavailable},
	}
	for _, constraint := range []string{"sku", "name", "description", "category", "price_cents"} {
		tests = append(tests, testCase{constraint + " constraint", &pgconn.PgError{Code: "23514", TableName: "products", ConstraintName: "products_" + constraint + "_check"}, catalog.ErrInvalidInput})
	}
	for _, code := range []string{"08000", "08001", "08003", "08004", "08006", "08007", "53300", "57P01", "57P02", "57P03", "57014", "55P03"} {
		tests = append(tests, testCase{code, &pgconn.PgError{Code: code}, catalog.ErrUnavailable})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, cause := range []error{tc.cause, fmt.Errorf("driver context: %w", tc.cause)} {
				got := classify(cause)
				require.ErrorIs(t, got, tc.cause, "retain the original error for logging")
				if tc.want != nil {
					require.ErrorIs(t, got, tc.want)
				} else {
					require.Equal(t, cause, got, "unrecognized failures must stay internal")
				}
			}
		})
	}
	require.NoError(t, classify(nil))
}

func TestClassificationAcrossRepositoryOperations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  error
	}{
		{"unavailable", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, catalog.ErrUnavailable},
		{"timeout", context.DeadlineExceeded, catalog.ErrUnavailable},
		{"invalid", &pgconn.PgError{Code: "23514", TableName: "products", ConstraintName: "products_name_check"}, catalog.ErrInvalidInput},
		{"SQL bug", &pgconn.PgError{Code: "42601"}, nil},
		{"scan bug", pgx.ScanArgError{ColumnIndex: 0, Err: io.ErrUnexpectedEOF}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := setup(t)
			p := fixture()
			want := tc.want
			if want == nil {
				want = tc.cause
			}
			ctx := context.Background()
			db.ExpectQuery("INSERT").WithArgs(pgxmock.AnyArg(), p.SKU, p.Name, p.Description, p.Category, p.PriceCents).WillReturnError(tc.cause)
			_, err := r.Create(ctx, input(p))
			require.ErrorIs(t, err, want)
			db.ExpectQuery("SELECT").WithArgs(p.ID.String()).WillReturnError(tc.cause)
			_, err = r.Get(ctx, p.ID)
			require.ErrorIs(t, err, want)
			db.ExpectQuery("UPDATE").WithArgs(p.ID.String(), p.SKU, p.Name, p.Description, p.Category, p.PriceCents).WillReturnError(tc.cause)
			_, err = r.Update(ctx, p.ID, input(p))
			require.ErrorIs(t, err, want)
			db.ExpectExec("DELETE").WithArgs(p.ID.String()).WillReturnError(tc.cause)
			require.ErrorIs(t, r.Delete(ctx, p.ID), want)
			db.ExpectQuery("SELECT").WithArgs(3).WillReturnError(tc.cause)
			_, err = r.List(ctx, catalog.Query{Limit: 2})
			require.ErrorIs(t, err, want)
			db.ExpectQuery("SELECT").WithArgs(3).WillReturnRows(row(p).RowError(0, tc.cause)).RowsWillBeClosed()
			_, err = r.List(ctx, catalog.Query{Limit: 2})
			require.ErrorIs(t, err, want)
		})
	}
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
	require.NotErrorIs(t, err, catalog.ErrUnavailable)
	db.ExpectQuery("SELECT").WithArgs(3).WillReturnRows(row(fixture()).RowError(0, context.Canceled)).RowsWillBeClosed()
	_, err = r.List(ctx, q)
	require.ErrorIs(t, err, context.Canceled)
}
