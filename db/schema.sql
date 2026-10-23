-- Skema demo toko online.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

DROP TABLE IF EXISTS reviews;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS categories;

CREATE TABLE categories (
    id   bigserial PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE products (
    id          bigserial PRIMARY KEY,
    category_id bigint NOT NULL REFERENCES categories (id),
    name        text   NOT NULL,
    price       bigint NOT NULL,
    stock       int    NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reviews (
    id         bigserial PRIMARY KEY,
    product_id bigint   NOT NULL REFERENCES products (id),
    rating     smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body       text,
    created_at timestamptz NOT NULL DEFAULT now()
);
