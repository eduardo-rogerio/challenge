# Todo o desenvolvimento roda em containers; só Docker e Docker Compose são necessários no host.
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)

DC  := docker compose
DEV := $(DC) run --rm dev

.PHONY: build fmt vet test test-race test-integration tidy migrate-up migrate-down up down

build:
	$(DEV) go build ./...

fmt:
	$(DEV) gofmt -l -w .

vet:
	$(DEV) go vet ./...

test:
	$(DEV) go test ./...

test-race:
	$(DEV) go test -race ./...

tidy:
	$(DEV) go mod tidy

# Integração: PostgreSQL e LocalStack reais, sem mocks. Os pacotes rodam em série (-p 1) porque
# compartilham a base e há workers que varrem as pendências de qualquer teste.
test-integration:
	$(DC) up -d --wait postgres localstack
	$(DC) run --rm queues
	$(DC) run --rm migrate up
	$(DEV) go test -race -p 1 -tags integration ./...

migrate-up:
	$(DC) run --rm migrate up

migrate-down:
	$(DC) run --rm migrate down 1

up:
	$(DC) up --build

down:
	$(DC) down
