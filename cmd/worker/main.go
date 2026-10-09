// Command worker é o executável dos workers (outbox, referências pendentes). Por ora apenas valida a
// configuração, conecta ao banco e aguarda o sinal de parada; os workers se registram em bootstrap.Worker.
package main

import (
	"fmt"
	"os"

	"go.uber.org/fx"

	"desafio/internal/bootstrap"
	"desafio/internal/platform/config"
	"desafio/internal/platform/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel)
	return bootstrap.Run(fx.New(bootstrap.Worker(cfg, log)))
}
