package catalog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func ptr(n int64) *int64 { return &n }

func validInput() Input {
	return Input{SKU: "SHOE-01_A", Name: "Running shoe", Category: "running-shoes", PriceCents: ptr(100)}
}

func TestInputValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Input)
		valid  bool
	}{
		{"valid", func(*Input) {}, true},
		{"free", func(in *Input) { in.PriceCents = ptr(0) }, true},
		{"upper boundaries", func(in *Input) {
			in.PriceCents = ptr(MaxPrice)
			in.Name = strings.Repeat("é", 200)
			in.Description = strings.Repeat("é", 5000)
			in.SKU = strings.Repeat("A", 64)
			in.Category = strings.Repeat("a", 64)
		}, true},
		{"missing price", func(in *Input) { in.PriceCents = nil }, false},
		{"negative price", func(in *Input) { in.PriceCents = ptr(-1) }, false},
		{"price too large", func(in *Input) { in.PriceCents = ptr(MaxPrice + 1) }, false},
		{"missing SKU", func(in *Input) { in.SKU = "" }, false},
		{"lowercase SKU", func(in *Input) { in.SKU = "shoe" }, false},
		{"SKU punctuation", func(in *Input) { in.SKU = "-SHOE" }, false},
		{"SKU too long", func(in *Input) { in.SKU = strings.Repeat("A", 65) }, false},
		{"name missing", func(in *Input) { in.Name = "" }, false},
		{"name blank", func(in *Input) { in.Name = " \t\n\u2003" }, false},
		{"name too long", func(in *Input) { in.Name = strings.Repeat("é", 201) }, false},
		{"description too long", func(in *Input) { in.Description = strings.Repeat("é", 5001) }, false},
		{"category missing", func(in *Input) { in.Category = "" }, false},
		{"category uppercase", func(in *Input) { in.Category = "Shoes" }, false},
		{"category double hyphen", func(in *Input) { in.Category = "running--shoes" }, false},
		{"category too long", func(in *Input) { in.Category = strings.Repeat("a", 65) }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.change(&in)
			if tc.valid {
				require.NoError(t, in.Validate())
			} else {
				require.Error(t, in.Validate())
			}
		})
	}
}
