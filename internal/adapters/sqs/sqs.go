// Package sqs cria o cliente SQS e o health check da fila principal.
package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// New cria o cliente; endpoint vazio usa o endpoint padrão da AWS.
func New(ctx context.Context, region, endpoint string) (*awssqs.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("carregar configuração AWS: %w", err)
	}
	return awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	}), nil
}

type Checker struct {
	Client *awssqs.Client
	Queue  string
}

func (Checker) Name() string { return "sqs" }

// Check confirma que a fila existe e responde.
func (c Checker) Check(ctx context.Context) error {
	u, err := c.Client.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(c.Queue)})
	if err != nil {
		return fmt.Errorf("obter URL da fila %s: %w", c.Queue, err)
	}
	_, err = c.Client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       u.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return fmt.Errorf("atributos da fila %s: %w", c.Queue, err)
	}
	return nil
}
