//go:build integration

package sqs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"desafio/internal/adapters/sqs"
)

func TestCheckerAgainstLocalStack(t *testing.T) {
	endpoint := os.Getenv("AWS_ENDPOINT_URL")
	if endpoint == "" {
		t.Skip("AWS_ENDPOINT_URL não definida")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := sqs.New(ctx, os.Getenv("AWS_REGION"), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := (sqs.Checker{Client: client, Queue: "wager-transactions.fifo"}).Check(ctx); err != nil {
		t.Fatalf("fila principal: %v", err)
	}
	if err := (sqs.Checker{Client: client, Queue: "nao-existe.fifo"}).Check(ctx); err == nil {
		t.Fatal("esperava erro para fila inexistente")
	}
}
