package catalog

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

const MaxPrice int64 = 1_000_000_000_000

var (
	ErrNotFound     = errors.New("product not found")
	ErrConflict     = errors.New("SKU already exists")
	ErrInvalidInput = errors.New("invalid product input")
	ErrUnavailable  = errors.New("database unavailable")
	validate        = validator.New(validator.WithRequiredStructEnabled())
	skuPattern      = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,63}$`)
	categoryPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

type Product struct {
	ID          uuid.UUID `json:"id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	PriceCents  int64     `json:"price_cents"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Input is a full replacement. A pointer distinguishes a free product from an
// omitted or null price. Descriptions intentionally default to the empty string.
type Input struct {
	SKU         string `json:"sku" validate:"required,max=64"`
	Name        string `json:"name" validate:"required,max=200"`
	Description string `json:"description" validate:"max=5000"`
	Category    string `json:"category" validate:"required,max=64"`
	PriceCents  *int64 `json:"price_cents" validate:"required,gte=0,lte=1000000000000"`
}

func (in Input) Validate() error {
	for _, value := range []string{in.SKU, in.Name, in.Description, in.Category} {
		if strings.ContainsRune(value, '\x00') {
			return ErrInvalidInput
		}
	}
	if err := validate.Struct(in); err != nil {
		return errors.New("required fields or field limits are invalid")
	}
	if !skuPattern.MatchString(in.SKU) {
		return errors.New("sku must contain uppercase letters, digits, underscores or hyphens")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name must not be blank")
	}
	return ValidateCategory(in.Category)
}

func ValidateCategory(category string) error {
	if len(category) > 64 || !categoryPattern.MatchString(category) {
		return errors.New("category must be a lowercase slug of 1 to 64 characters")
	}
	return nil
}

type Page struct {
	Items      []Product `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}
