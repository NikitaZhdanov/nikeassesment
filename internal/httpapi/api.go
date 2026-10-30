package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/nikitazhdanov/nikeassesment/internal/catalog"
)

type Repository interface {
	Create(context.Context, catalog.Input) (catalog.Product, error)
	Get(context.Context, uuid.UUID) (catalog.Product, error)
	Update(context.Context, uuid.UUID, catalog.Input) (catalog.Product, error)
	Delete(context.Context, uuid.UUID) error
	List(context.Context, catalog.Query) ([]catalog.Product, error)
}

type API struct {
	repo Repository
	ping func(context.Context) error
	log  *slog.Logger
}

func New(repo Repository, ping func(context.Context) error, log *slog.Logger) http.Handler {
	a := &API{repo: repo, ping: ping, log: log}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, a.observe)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", a.ready)
	r.Post("/v1/products", a.create)
	r.Get("/v1/products", a.list)
	r.Get("/v1/products/{id}", a.get)
	r.Put("/v1/products/{id}", a.update)
	r.Delete("/v1/products/{id}", a.delete)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "not_found", "route not found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		var allowed []string
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			if r.Match(chi.NewRouteContext(), method, req.URL.Path) {
				allowed = append(allowed, method)
			}
		}
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		fail(w, 405, "method_not_allowed", "method not allowed")
	})
	return r
}

func (a *API) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("X-Request-ID", middleware.GetReqID(r.Context()))
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				a.log.Error("request panic", "request_id", middleware.GetReqID(r.Context()), "panic", recovered)
				fail(ww, 500, "internal_error", "internal server error")
			}
			a.log.Info("request", "request_id", middleware.GetReqID(r.Context()), "method", r.Method,
				"path", r.URL.Path, "status", ww.Status(), "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(ww, r.WithContext(ctx))
	})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.ping(ctx); err != nil {
		a.failure(w, r, fmt.Errorf("%w: %w", catalog.ErrUnavailable, err))
		return
	}
	respond(w, 200, map[string]string{"status": "ready"})
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	in, ok := decode(w, r)
	if !ok {
		return
	}
	p, err := a.repo.Create(r.Context(), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/products/"+p.ID.String())
	respond(w, 201, p)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	id, ok := productID(w, r)
	if !ok {
		return
	}
	p, err := a.repo.Get(r.Context(), id)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, p)
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	id, ok := productID(w, r)
	if !ok {
		return
	}
	in, ok := decode(w, r)
	if !ok {
		return
	}
	p, err := a.repo.Update(r.Context(), id, in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, p)
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := productID(w, r)
	if !ok {
		return
	}
	if err := a.repo.Delete(r.Context(), id); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	q, err := catalog.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "validation_error", err.Error())
		return
	}
	products, err := a.repo.List(r.Context(), q)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, catalog.NewPage(products, q))
}

func productID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, 400, "validation_error", "id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request) (catalog.Input, bool) {
	var in catalog.Input
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, 415, "unsupported_media_type", "Content-Type must be application/json")
		return in, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	// Read the entire bounded payload, including trailing whitespace, before
	// decoding: encoding/json would silently replace malformed UTF-8.
	body, err := io.ReadAll(r.Body)
	if err == nil {
		if !utf8.Valid(body) {
			err = errors.New("invalid UTF-8")
		} else {
			err = decodeProduct(body, &in)
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, 413, "payload_too_large", "body exceeds 64 KiB")
		} else {
			fail(w, 400, "validation_error", "invalid product JSON or field values")
		}
		return in, false
	}
	return in, true
}

// Decode object members individually because encoding/json's struct matching
// accepts case variants and silently overwrites duplicate keys. Decoded keys
// also catch duplicates expressed with JSON Unicode escapes.
func decodeProduct(body []byte, in *catalog.Input) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start != json.Delim('{') {
		return errors.New("product must be an object")
	}
	fields := map[string]any{
		"sku": &in.SKU, "name": &in.Name, "description": &in.Description,
		"category": &in.Category, "price_cents": &in.PriceCents,
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		field, known := fields[name]
		if !ok || !known {
			return errors.New("unknown or duplicate product property")
		}
		delete(fields, name)
		if err := decoder.Decode(field); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing JSON or multiple values")
	}
	return in.Validate()
}

func (a *API) failure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		fail(w, 404, "not_found", "product not found")
	case errors.Is(err, catalog.ErrConflict):
		fail(w, 409, "conflict", "SKU already exists")
	case errors.Is(err, catalog.ErrInvalidInput):
		fail(w, 400, "validation_error", "invalid product field values")
	default:
		a.log.Error("repository failure", "request_id", middleware.GetReqID(r.Context()), "error", err)
		if errors.Is(err, catalog.ErrUnavailable) {
			fail(w, 503, "unavailable", "service temporarily unavailable")
		} else {
			fail(w, 500, "internal_error", "internal server error")
		}
	}
}

func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// All responses are JSON-safe DTOs. Transport write failures cannot be repaired
	// after headers are sent; the client's context/connection controls their lifetime.
	_ = json.NewEncoder(w).Encode(value)
}
