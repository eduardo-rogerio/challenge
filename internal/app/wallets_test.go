package app

import (
	"errors"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	for _, seq := range []int64{0, 1, 42, 1 << 40} {
		got, err := decodeCursor(encodeCursor(seq))
		if err != nil || got != seq {
			t.Errorf("seq %d: %d, %v", seq, got, err)
		}
	}
	if got, err := decodeCursor(""); err != nil || got != 0 {
		t.Errorf("cursor vazio: %d, %v", got, err)
	}
}

func TestCursorInvalid(t *testing.T) {
	for _, c := range []string{"abc", "!!!", "djE6eA", encodeCursorRaw("v2:5"), encodeCursorRaw("v1:-1"), encodeCursorRaw("v1:x"), encodeCursorRaw("5")} {
		if _, err := decodeCursor(c); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("cursor %q: erro %v, esperado ErrInvalidInput", c, err)
		}
	}
}

func encodeCursorRaw(s string) string {
	// reutiliza a codificação base64url do cursor para montar valores malformados
	return encodeRaw([]byte(s))
}
