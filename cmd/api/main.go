// Command api é o executável HTTP: valida a configuração e executa a composição Fx de internal/bootstrap.
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
	// A configuração é validada antes de construir qualquer componente.
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel)
	return bootstrap.Run(fx.New(bootstrap.API(cfg, log)))
}
