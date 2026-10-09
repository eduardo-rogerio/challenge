package domain_test

import (
	"os/exec"
	"strings"
	"testing"
)

// O domínio deve permanecer independente de infraestrutura e composição.
func TestDomainHasNoInfrastructureDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "desafio/internal/domain").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{
		"net/http",
		"github.com/aws/",
		"github.com/jackc/pgx",
		"desafio/internal/adapters",
		"desafio/internal/platform",
		"desafio/internal/bootstrap",
		"go.uber.org/",
		"desafio/cmd",
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, f := range forbidden {
			if strings.HasPrefix(dep, f) {
				t.Errorf("domain depende de %q (proibido: %q)", dep, f)
			}
		}
	}
}
