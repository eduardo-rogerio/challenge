package bootstrap

import (
	"context"
	"fmt"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.uber.org/fx"

	httpadapter "desafio/internal/adapters/http"
	"desafio/internal/adapters/sqs"
	"desafio/internal/platform/config"
)

var messagingModule = fx.Module("messaging",
	fx.Provide(
		newSQSClient,
		fx.Annotate(newSQSChecker, fx.ResultTags(checkersGroup)),
	),
)

func newSQSClient(cfg config.Config) (*awssqs.Client, error) {
	client, err := sqs.New(context.Background(), cfg.AWSRegion, cfg.SQSEndpoint)
	if err != nil {
		return nil, fmt.Errorf("sqs: %w", err)
	}
	return client, nil
}

func newSQSChecker(client *awssqs.Client, cfg config.Config) httpadapter.Checker {
	return sqs.Checker{Client: client, Queue: cfg.SQSQueue}
}
