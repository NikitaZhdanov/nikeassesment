-- Run only in a disposable database. All fixture rows are rolled back.
\timing on
BEGIN;
INSERT INTO products (id, sku, name, description, category, price_cents)
SELECT md5('catalog-benchmark-' || n)::uuid,
       'BENCH-' || n,
       'Benchmark product ' || n,
       repeat('Representative product description. ', 5),
       'bench-' || (n % 100),
       (n::bigint * 7919) % 1000000
FROM generate_series(1, 300000) AS n;
ANALYZE products;

\echo 'Global first page'
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM products ORDER BY price_cents, id LIMIT 21;

\echo 'Category with inclusive price range'
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM products
WHERE category = 'bench-42' AND price_cents BETWEEN 100000 AND 900000
ORDER BY price_cents, id LIMIT 21;

\echo 'Deep cursor page'
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM products
WHERE (price_cents, id) > (900000, '00000000-0000-0000-0000-000000000000'::uuid)
ORDER BY price_cents, id LIMIT 21;

\echo 'Deep OFFSET comparison (not used by the API)'
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM products ORDER BY price_cents, id LIMIT 21 OFFSET 270000;
ROLLBACK;
