#!/bin/sh
# Cria a fila FIFO principal e a DLQ com redrive. Seguro para reexecução.
set -eu

QUEUE="${SQS_QUEUE_NAME:-wager-transactions.fifo}"
DLQ="${SQS_DLQ_NAME:-wager-transactions-dlq.fifo}"
MAX_RECEIVE="${SQS_MAX_RECEIVE_COUNT:-5}"

dlq_url=$(aws sqs create-queue --queue-name "$DLQ" \
  --attributes '{"FifoQueue":"true"}' --query QueueUrl --output text)
dlq_arn=$(aws sqs get-queue-attributes --queue-url "$dlq_url" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)

attrs=$(mktemp)
cat > "$attrs" <<JSON
{
  "FifoQueue": "true",
  "VisibilityTimeout": "30",
  "RedrivePolicy": "{\"deadLetterTargetArn\":\"$dlq_arn\",\"maxReceiveCount\":\"$MAX_RECEIVE\"}"
}
JSON

url=$(aws sqs create-queue --queue-name "$QUEUE" --attributes "file://$attrs" \
  --query QueueUrl --output text)
# create-queue falha com atributos divergentes; set-queue-attributes mantém a reexecução segura.
aws sqs set-queue-attributes --queue-url "$url" --attributes "file://$attrs"
echo "queues ready: $url (dlq: $dlq_url)"
