# Mantenha GO_VERSION igual à versão declarada em go.mod.
ARG GO_VERSION=1.26.0

# Ambiente de desenvolvimento: ferramentas Go usadas via `docker compose run dev`.
FROM golang:${GO_VERSION}-alpine AS dev
# build-base é necessário para `go test -race` (cgo).
RUN apk add --no-cache build-base git
ENV GOFLAGS=-buildvcs=false \
    GOCACHE=/go-cache/build \
    GOMODCACHE=/go-cache/mod \
    HOME=/tmp
RUN mkdir -p /go-cache/build /go-cache/mod && chmod -R 777 /go-cache
WORKDIR /app

FROM dev AS build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker

FROM alpine:3.22 AS runtime
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/api /out/worker /usr/local/bin/
USER app
EXPOSE 8080
CMD ["api"]
