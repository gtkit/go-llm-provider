package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"short masked entirely", "short", "****"},
		{"boundary eight", "eightchr", "****"},
		{"long keeps head and tail", "sk-1234567890wxyz", "sk-1****wxyz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, MaskSecret(tt.in))
		})
	}
}
