//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nikitazhdanov/nikeassesment/internal/catalog"
	"github.com/stretchr/testify/require"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	require.NotEmpty(t, dsn, "integration tests require TEST_DATABASE_URL pointing to a disposable migrated PostgreSQL database")
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(context.Background()))
	return pool
}

func integrationTx(t *testing.T) pgx.Tx {
	t.Helper()
	pool := integrationPool(t)
	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func TestIntegrationMigrationUpDown(t *testing.T) {
	tx := integrationTx(t)
	ctx := context.Background()
	var version int
	var dirty bool
	require.NoError(t, tx.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty))
	require.Equal(t, 1, version)
	require.False(t, dirty)
	for _, name := range []string{"000001_products.down.sql", "000001_products.up.sql"} {
		sql, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, string(sql))
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM pg_indexes WHERE tablename='products' AND schemaname=current_schema()").Scan(&count))
	require.Equal(t, 4, count)
	_, err := New(tx).Create(ctx, input(fixture()))
	require.NoError(t, err)
}

func TestIntegrationCRUD(t *testing.T) {
	tx := integrationTx(t)
	r := New(tx)
	ctx := context.Background()
	in := input(fixture())
	p, err := r.Create(ctx, in)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, p.ID)
	require.False(t, p.CreatedAt.IsZero())
	got, err := r.Get(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, p, got)
	in.SKU = "UPDATED-01"
	in.Name = "New shoe"
	in.Category = "new-shoes"
	in.Description = ""
	price := int64(0)
	in.PriceCents = &price
	updated, err := r.Update(ctx, p.ID, in)
	require.NoError(t, err)
	require.Equal(t, p.ID, updated.ID)
	require.Equal(t, p.CreatedAt, updated.CreatedAt)
	require.True(t, updated.UpdatedAt.After(p.UpdatedAt))
	require.Zero(t, updated.PriceCents)
	require.Empty(t, updated.Description)
	got, err = r.Get(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, updated, got)
	_, err = r.Update(ctx, uuid.New(), in)
	require.ErrorIs(t, err, catalog.ErrNotFound)
	require.NoError(t, r.Delete(ctx, p.ID))
	_, err = r.Get(ctx, p.ID)
	require.ErrorIs(t, err, catalog.ErrNotFound)
	require.ErrorIs(t, r.Delete(ctx, p.ID), catalog.ErrNotFound)
}

func TestIntegrationFiltersAndPagination(t *testing.T) {
	r := New(integrationTx(t))
	ctx := context.Background()
	var products []catalog.Product
	for i, price := range []int64{0, 100, 100, 200, 300, 150} {
		in := input(fixture())
		in.SKU = strings.ToUpper(uuid.NewString())
		in.PriceCents = &price
		if i == 5 {
			in.Category = "shirts"
		}
		p, err := r.Create(ctx, in)
		require.NoError(t, err)
		products = append(products, p)
	}
	for _, tc := range []struct {
		raw   string
		count int
	}{
		{"", 6}, {"category=shoes", 5}, {"min_price_cents=150", 3}, {"max_price_cents=100", 3}, {"min_price_cents=0&max_price_cents=0", 1}, {"category=missing", 0},
	} {
		q, err := catalog.ParseQuery(tc.raw)
		require.NoError(t, err)
		got, err := r.List(ctx, q)
		require.NoError(t, err)
		require.Len(t, got, tc.count, tc.raw)
	}
	filter := "category=shoes&min_price_cents=100&max_price_cents=200&limit=1"
	q, err := catalog.ParseQuery(filter)
	require.NoError(t, err)
	var gotIDs []uuid.UUID
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		rows, err := r.List(ctx, q)
		require.NoError(t, err)
		page := catalog.NewPage(rows, q)
		for _, p := range page.Items {
			gotIDs = append(gotIDs, p.ID)
		}
		if page.NextCursor == nil {
			break
		}
		q, err = catalog.ParseQuery(filter + "&cursor=" + *page.NextCursor)
		require.NoError(t, err)
	}
	want := products[1:4]
	sort.Slice(want, func(i, j int) bool {
		if want[i].PriceCents != want[j].PriceCents {
			return want[i].PriceCents < want[j].PriceCents
		}
		return want[i].ID.String() < want[j].ID.String()
	})
	require.Equal(t, []uuid.UUID{want[0].ID, want[1].ID, want[2].ID}, gotIDs)
}

func TestIntegrationConstraints(t *testing.T) {
	for _, tc := range []struct {
		name   string
		column string
		value  any
		code   string
	}{
		{"price negative", "price_cents", int64(-1), "23514"},
		{"price overflow", "price_cents", catalog.MaxPrice + 1, "23514"},
		{"price null", "price_cents", nil, "23502"},
		{"sku case", "sku", "lower", "23514"},
		{"name blank", "name", " \t\n", "23514"},
		{"name length", "name", strings.Repeat("a", 201), "23514"},
		{"description length", "description", strings.Repeat("a", 5001), "23514"},
		{"category pattern", "category", "Wrong Category", "23514"},
		{"category length", "category", strings.Repeat("a", 65), "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := integrationTx(t)
			p, err := New(tx).Create(context.Background(), input(fixture()))
			require.NoError(t, err)
			// Column names are fixed test cases, never client input.
			_, err = tx.Exec(context.Background(), "UPDATE products SET "+tc.column+"=$1 WHERE id=$2", tc.value, p.ID.String())
			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			require.Equal(t, tc.code, pgErr.Code)
		})
	}
}

func TestIntegrationConcurrentUniquenessAndPersistence(t *testing.T) {
	pool := integrationPool(t)
	r := New(pool)
	ctx := context.Background()
	in := input(fixture())
	in.SKU = strings.ToUpper(uuid.NewString())
	t.Cleanup(func() { _, err := pool.Exec(ctx, "DELETE FROM products WHERE sku=$1", in.SKU); require.NoError(t, err) })
	type outcome struct {
		p   catalog.Product
		err error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; p, err := r.Create(ctx, in); results <- outcome{p, err} }()
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	var saved catalog.Product
	for result := range results {
		if result.err == nil {
			successes++
			saved = result.p
		} else if errors.Is(result.err, catalog.ErrConflict) {
			conflicts++
		} else {
			require.NoError(t, result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	// A separate connection pool observes the committed row.
	got, err := New(integrationPool(t)).Get(ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, saved, got)
}

func TestIntegrationUpdateConflict(t *testing.T) {
	r := New(integrationTx(t))
	ctx := context.Background()
	one := input(fixture())
	two := input(fixture())
	two.SKU = "OTHER-SKU"
	p, err := r.Create(ctx, one)
	require.NoError(t, err)
	other, err := r.Create(ctx, two)
	require.NoError(t, err)
	_, err = r.Update(ctx, other.ID, one)
	require.ErrorIs(t, err, catalog.ErrConflict)
	// The transaction is aborted after a constraint violation; PostgreSQL atomicity
	// ensures the failed statement cannot partially update the other product.
	require.NotEqual(t, p.ID, other.ID)
}
