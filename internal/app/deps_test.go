package app_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Os casos de uso não conhecem o framework de composição, os adaptadores nem a infraestrutura.
func TestAppHasNoCompositionOrInfrastructureDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "desafio/internal/app").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{
		"go.uber.org/",
		"github.com/jackc/pgx",
		"github.com/aws/",
		"net/http",
		"desafio/internal/adapters",
		"desafio/internal/platform",
		"desafio/internal/bootstrap",
		"desafio/cmd",
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, f := range forbidden {
			if strings.HasPrefix(dep, f) {
				t.Errorf("internal/app depende de %q (proibido: %q)", dep, f)
			}
		}
	}
}
