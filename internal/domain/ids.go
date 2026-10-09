package domain

import (
	"fmt"
	"regexp"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ID é um identificador UUID. O valor zero não é um ID válido.
type ID string

func ParseID(s string) (ID, error) {
	id := ID(s)
	if !id.valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return id, nil
}

func (id ID) valid() bool    { return uuidRE.MatchString(string(id)) }
func (id ID) String() string { return string(id) }
func (id ID) IsZero() bool   { return id == "" }
