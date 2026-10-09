package ids

import (
	"regexp"
	"testing"
	"time"

	"desafio/internal/domain"
)

var v7RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUUIDv7Format(t *testing.T) {
	id := Generator{}.NewID()
	if !v7RE.MatchString(id.String()) {
		t.Fatalf("%q não é um UUIDv7", id)
	}
	if _, err := domain.ParseID(id.String()); err != nil {
		t.Fatalf("o domínio rejeitou o id: %v", err)
	}
}

func TestUUIDv7OrderedByTime(t *testing.T) {
	g := Generator{}
	first := g.NewID()
	time.Sleep(3 * time.Millisecond)
	second := g.NewID()
	if !(first.String() < second.String()) {
		t.Fatalf("%s deveria vir antes de %s", first, second)
	}
}

func TestUUIDv7Unique(t *testing.T) {
	g := Generator{}
	seen := make(map[domain.ID]bool, 10000)
	for range 10000 {
		id := g.NewID()
		if seen[id] {
			t.Fatalf("id repetido: %s", id)
		}
		seen[id] = true
	}
}
