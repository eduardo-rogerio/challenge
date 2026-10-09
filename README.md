# Desafio Backend — Processamento Distribuído de Apostas (Go)

Serviço em Go que movimenta carteiras de jogadores a partir de operações de provedores de jogos (`BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`), com garantias financeiras em ambiente distribuído: dinheiro exato, ledger imutável, idempotência persistente e concorrência correta entre várias instâncias.

Enunciado: <https://github.com/junglegaming/backend-challenge-go>. Decisões de projeto: [ARCHITECTURE.md](ARCHITECTURE.md).

## Sumário

1. [O que está disponível](#o-que-está-disponível)
2. [Pré-requisitos](#pré-requisitos)
3. [Início rápido](#início-rápido)
4. [Serviços do Docker Compose](#serviços-do-docker-compose)
5. [Executando](#executando)
6. [Variáveis de ambiente](#variáveis-de-ambiente)
7. [Migrations](#migrations)
8. [Filas SQS](#filas-sqs)
9. [Referência da API](#referência-da-api)
10. [Fluxos de exemplo](#fluxos-de-exemplo)
11. [Observabilidade](#observabilidade)
12. [Testes e verificação](#testes-e-verificação)
13. [Simulando falhas e várias instâncias](#simulando-falhas-e-várias-instâncias)
14. [Estrutura do repositório](#estrutura-do-repositório)
15. [Solução de problemas](#solução-de-problemas)
16. [O que ainda não existe](#o-que-ainda-não-existe)

## O que está disponível

**API HTTP** (`net/http`), sem dependência de framework web:

- Abertura e consulta de carteiras; ledger paginado por cursor opaco; reconciliação do saldo contra o ledger (sem alterar dados).
- Envio de operações `BET`, `WIN`, `LOSS`, `REFUND` e `ROLLBACK`, processadas de forma síncrona e atômica, com consulta por id interno e por provedor + id externo.
- Health checks (`/health/live`, `/health/ready`), métricas Prometheus (`/metrics`) e logs JSON com `correlationId`.

**Garantias financeiras**

- **Dinheiro exato:** `Money` em unidades mínimas (`int64`), duas casas decimais, moeda ISO de 3 letras; nunca `float`. Parsing estrito (rejeita `"10"`, `"1.5"`, `"-1.00"`, `"1e3"`, `NaN`), com proteção contra overflow e mistura de moedas.
- **Ledger append-only:** `UPDATE`, `DELETE` e `TRUNCATE` são bloqueados por trigger; correções exigem novos lançamentos. Saldo e lançamento são gravados no mesmo commit.
- **Invariantes no banco:** saldo ≥ 0, uma carteira por (jogador, moeda), uma operação externa por (provedor, id externo), uma única reversão bem-sucedida por transação referenciada, aritmética do ledger por `CHECK`.
- **Concorrência:** lock pessimista por carteira (`SELECT … FOR UPDATE`) — operações da mesma carteira são serializadas, carteiras diferentes avançam em paralelo, sem lock global. Verificado com três instâncias de pools independentes.
- **Idempotência persistente:** header `Idempotency-Key` obrigatório, hash canônico do conteúdo, replay do resultado original (inclusive o saldo observado), conflito para chave reutilizada com outro conteúdo, retomada de execuções abandonadas por lease.
- **Reversões:** `REFUND` devolve uma `BET`; `ROLLBACK` desfaz `BET`, `WIN` ou `REFUND`; cada transação recebe no máximo uma reversão bem-sucedida.
- **Reversão antes da referência:** gravada como `PENDING_REFERENCE` (HTTP 202) e retomada pelo **worker de referências pendentes**, com retry, backoff exponencial, TTL e coordenação entre instâncias (`FOR UPDATE SKIP LOCKED` + lease).

**Operação**

- Composição por **Uber Fx** (`fx.Module`, `fx.Lifecycle`), shutdown ordenado (servidor → workers → pool) e falha rápida na inicialização.
- Docker Compose com PostgreSQL, LocalStack (SQS), migrations, filas, API e worker escaláveis.
- Todo o desenvolvimento roda em containers: não é preciso instalar Go.

## Pré-requisitos

- **Docker** e **Docker Compose** v2.24 ou superior.
- `make`.

Go **não** é necessário no host: os comandos de desenvolvimento rodam no serviço `dev` do Compose (Go 1.26, a mesma versão de `go.mod` e do `Dockerfile`).

## Início rápido

```sh
git clone <este repositório> && cd desafio
docker compose up --build        # ou: make up
```

Quando os serviços estiverem saudáveis, em outro terminal:

```sh
curl localhost:8080/health/ready
# {"status":"ok","checks":{"postgres":"ok","sqs":"ok"}}
```

Para parar: `Ctrl+C` (ou `make down`). Para apagar também os dados do banco: `docker compose down -v`.

> A API publica a porta `8080`. Com várias réplicas, o Compose usa a faixa `8080–8089`; descubra a porta de cada uma com `docker compose port --index 1 app 8080`.

## Serviços do Docker Compose

| Serviço | O que faz | Porta no host |
| --- | --- | --- |
| `postgres` | PostgreSQL 17 (usuário/senha/banco `app`) | `5432` |
| `localstack` | SQS emulado | `4566` |
| `queues` | Cria `wager-transactions.fifo` e `wager-transactions-dlq.fifo`; termina ao concluir | — |
| `migrate` | Aplica as migrations; termina ao concluir | — |
| `app` | API HTTP (`api`) | `8080–8089` |
| `worker` | Worker de referências pendentes (`worker`) | — |
| `dev` | Ferramentas Go (build, test, vet, fmt); só com o profile `tools`, usado pelo `make` | — |

A aplicação **nunca** executa migrations sozinha: o serviço `migrate` roda uma vez antes de `app` e `worker`, para que várias réplicas não disputem a migração.

## Executando

```sh
# tudo (API + worker + dependências), em primeiro plano
docker compose up --build

# em segundo plano, esperando ficarem saudáveis
docker compose up -d --build --wait app worker

# várias instâncias
docker compose up -d --build --scale app=3 --scale worker=2 --wait app worker

# logs (JSON)
docker compose logs -f app worker

# parar / parar e apagar os dados
docker compose down
docker compose down -v
```

Cada réplica da API é independente e compartilha apenas o PostgreSQL; é seguro enviar operações para qualquer uma delas.

## Variáveis de ambiente

Os valores locais estão em [`.env.example`](.env.example) e são carregados pelo Compose. Um arquivo `.env` (ignorado pelo git) sobrescreve qualquer um deles.

| Variável | Obrigatória | Padrão | Descrição |
| --- | --- | --- | --- |
| `DATABASE_URL` | sim | — | URL de conexão do PostgreSQL |
| `AWS_REGION` | sim | — | Região do SQS |
| `AWS_ENDPOINT_URL` | não | endpoint AWS | Endpoint do SQS (LocalStack localmente) |
| `SQS_QUEUE_NAME` | não | `wager-transactions.fifo` | Fila verificada no `readiness` |
| `HTTP_ADDR` | não | `:8080` | Endereço do servidor HTTP |
| `LOG_LEVEL` | não | `info` | `debug`, `info`, `warn`, `error` (o detalhe de injeção do Fx só aparece em `debug`) |
| `SHUTDOWN_TIMEOUT` | não | `20s` | Prazo do shutdown gracioso |
| `CHECK_TIMEOUT` | não | `2s` | Prazo dos checks de dependência e da validação do banco na inicialização |
| `IDEMPOTENCY_LEASE` | não | `30s` | Prazo de uma execução idempotente em andamento antes de poder ser retomada |

`SQS_DLQ_NAME` e `SQS_MAX_RECEIVE_COUNT` (padrão `5`) são usadas só pelo provisionamento das filas. `AWS_ACCESS_KEY_ID` e `AWS_SECRET_ACCESS_KEY` (`test`) são exigidas pelo SDK, mas ignoradas pelo LocalStack.

A configuração é validada antes de qualquer componente ser construído: uma variável obrigatória ausente ou inválida encerra o processo com código 1 e uma mensagem que nomeia o campo.

### Worker de referências pendentes

| Variável | Padrão | Descrição |
| --- | --- | --- |
| `REFERENCE_MAX_ATTEMPTS` | `10` | Tentativas antes de expirar |
| `REFERENCE_TTL` | `1h` | Prazo absoluto desde a criação da pendência |
| `REFERENCE_BASE_DELAY` | `5s` | Atraso depois da primeira tentativa sem sucesso (dobra a cada tentativa) |
| `REFERENCE_MAX_DELAY` | `5m` | Teto do atraso entre tentativas |
| `REFERENCE_POLL_INTERVAL` | `1s` | Intervalo entre varreduras |
| `REFERENCE_CLAIM_LEASE` | `30s` | Por quanto tempo uma pendência reivindicada fica reservada a uma instância |
| `REFERENCE_BATCH_SIZE` | `20` | Pendências reivindicadas por varredura |

Para observar o retry em segundos, crie um `.env` com valores curtos:

```sh
REFERENCE_MAX_ATTEMPTS=3
REFERENCE_BASE_DELAY=1s
REFERENCE_MAX_DELAY=2s
REFERENCE_POLL_INTERVAL=500ms
```

## Migrations

Arquivos em `migrations/` (`NNNN_nome.up.sql` e `.down.sql`), aplicados com `golang-migrate`:

```sh
make migrate-up      # aplica todas as pendentes
make migrate-down    # reverte a última
```

| Migration | Conteúdo |
| --- | --- |
| `0001` | Baseline (`pgcrypto`) |
| `0002` | `idempotency_keys` |
| `0003` | `wallets`, `wager_transactions`, `wallet_ledger_entries` (constraints, índices únicos parciais, ledger append-only) |
| `0004` | Agendamento das pendências (`reference_attempts`, `next_attempt_at`) |

O Compose aplica as migrations automaticamente na subida.

## Filas SQS

O serviço `queues` provisiona, de forma idempotente, no LocalStack:

- `wager-transactions.fifo` — fila principal, com política de redrive para a DLQ após 5 recebimentos e visibility timeout de 30 s;
- `wager-transactions-dlq.fifo` — fila de mensagens mortas.

Hoje as filas são apenas verificadas pelo `readiness`; o consumidor SQS ainda não existe (veja [O que ainda não existe](#o-que-ainda-não-existe)).

## Referência da API

Base: `http://localhost:8080`. Corpos em JSON. Valores monetários são **sempre strings**: `{"amount":"25.00","currency":"BRL"}`.

| Rota | Descrição | Sucesso |
| --- | --- | --- |
| `GET /health/live` | Processo vivo | 200 |
| `GET /health/ready` | PostgreSQL e SQS respondem; indica a dependência com falha | 200 / 503 |
| `GET /metrics` | Métricas Prometheus | 200 |
| `POST /wallets` | Abre carteira (`playerId`, `initialBalance`) | 201 |
| `GET /wallets/{walletId}` | Consulta carteira (saldo e versão) | 200 |
| `GET /wallets/{walletId}/ledger?cursor=&limit=` | Ledger paginado (`limit` 1–100, padrão 50; `nextCursor` nulo na última página) | 200 |
| `POST /wallets/{walletId}/reconciliation` | Reconstrói o saldo a partir do ledger e compara com o armazenado | 200 |
| `POST /wagering/transactions` | Envia uma operação (exige `Idempotency-Key`) | 200 / 202 / 422 |
| `GET /wagering/transactions/{transactionId}` | Consulta por id interno | 200 |
| `GET /providers/{providerId}/wagering/transactions/{externalTransactionId}` | Consulta por provedor e id externo | 200 |

### `POST /wallets`

```json
{ "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "initialBalance": { "amount": "1000.00", "currency": "BRL" } }
```

Com saldo positivo grava, no mesmo commit, a carteira (versão `1`), a transação interna `OPENING` e o lançamento de crédito. Com saldo `0.00`, apenas a carteira. Um segundo pedido para o mesmo jogador e moeda responde `409 CONFLICT`.

### `POST /wagering/transactions`

Header obrigatório `Idempotency-Key` (1 a 255 caracteres ASCII imprimíveis, sem espaços). Corpo:

```json
{ "providerId": "provider-a", "externalTransactionId": "bet-1",
  "playerId": "<uuid>", "walletId": "<uuid>", "roundId": "round-1", "gameId": "fortune-chimp",
  "kind": "BET", "money": { "amount": "25.00", "currency": "BRL" },
  "referenceExternalTransactionId": "..." }
```

`referenceExternalTransactionId` é obrigatório em `REFUND` e `ROLLBACK`. Regras por tipo:

| Tipo | Efeito | Observações |
| --- | --- | --- |
| `BET` | Debita | Valor positivo; saldo suficiente |
| `WIN` | Credita | Valor positivo; referência opcional a uma `BET` da mesma rodada |
| `LOSS` | Nenhuma movimentação | Valor exatamente `0.00`; não gera lançamento nem muda a versão |
| `REFUND` | Credita | Devolve integralmente uma `BET` processada |
| `ROLLBACK` | Movimento contrário | Desfaz integralmente uma `BET`, `WIN` ou `REFUND` processada |

Resposta:

```json
{ "transactionId": "...", "status": "PROCESSED",
  "balance": { "amount": "975.00", "currency": "BRL" }, "idempotentReplay": false }
```

| `status` | HTTP | Significado |
| --- | --- | --- |
| `PROCESSED` | 200 | Concluída; `balance` é o saldo observado no processamento |
| `PENDING_REFERENCE` | 202 | Reversão antes da referência; será retomada pelo worker |
| `REJECTED` | 422 | Recusada por regra de negócio; o corpo traz `failureCode` |

### Idempotência

- Mesma chave e mesmo conteúdo: devolve o resultado original com `idempotentReplay: true` (inclusive o saldo da época), sem nova movimentação. Se a operação era pendente e já foi concluída pelo worker, o replay mostra o estado atual.
- Mesma chave com conteúdo diferente, ou o mesmo `(providerId, externalTransactionId)` sob outra chave: `409 IDEMPOTENCY_CONFLICT`.
- Outra requisição com a mesma chave ainda em execução: `409 REQUEST_IN_PROGRESS` (tente de novo).

### Erros

Formato: `{"error":{"code":"...","message":"..."}}`.

| Situação | HTTP | `code` |
| --- | --- | --- |
| Entrada inválida (formato, header ausente, ou valor incompatível com o tipo: `BET` com zero, `LOSS` com valor) | 400 | `INVALID_REQUEST` |
| Recurso inexistente (inclui carteira desconhecida no envio) | 404 | `NOT_FOUND` |
| Conflito (por exemplo, carteira já existente) | 409 | `CONFLICT` |
| Chave ou operação já usada com outro conteúdo | 409 | `IDEMPOTENCY_CONFLICT` |
| Mesma chave ainda em execução | 409 | `REQUEST_IN_PROGRESS` |
| Rejeição de negócio | 422 | — (veja `failureCode`) |
| Falha interna (sem detalhes internos) | 500 | `INTERNAL` |
| Banco indisponível ou lock expirado | 503 | `UNAVAILABLE` |

### Códigos de falha (`failureCode`)

| Código | Quando | Natureza |
| --- | --- | --- |
| `INSUFFICIENT_FUNDS` | `BET` sem saldo | definitiva |
| `REVERSAL_INSUFFICIENT_FUNDS` | Reversão que debitaria mais que o saldo | definitiva |
| `WALLET_MISMATCH` | Carteira, jogador ou moeda divergem da carteira | corrigível |
| `REFERENCE_MISMATCH` | Provedor, jogador, carteira, moeda ou rodada divergem da referência | corrigível |
| `REFERENCE_AMOUNT_MISMATCH` | Valor da reversão difere do referenciado (não há reversão parcial) | corrigível |
| `INVALID_REFERENCE_KIND` | `REFUND` de algo que não é `BET`; `ROLLBACK` de `LOSS` ou `ROLLBACK` | corrigível |
| `REFERENCE_ALREADY_REVERSED` | A referência já tem uma reversão bem-sucedida | definitiva |
| `REFERENCE_NOT_PROCESSED` | A referência terminou `REJECTED`/`FAILED` (ou existe, mas nunca concluiu ao expirar) | definitiva |
| `REFERENCE_NOT_FOUND` | A referência nunca chegou (tentativas ou TTL esgotados) | definitiva |

Os estados de uma transação são `PENDING`, `PENDING_REFERENCE`, `PROCESSED`, `REJECTED` e `FAILED`; os três últimos são terminais.

## Fluxos de exemplo

Os exemplos usam `jq` opcionalmente; troque `<...>` pelos valores devolvidos.

### Carteira, aposta e consulta

```sh
# 1) abre uma carteira com 100.00 BRL
curl -s -X POST localhost:8080/wallets \
  -d '{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"100.00","currency":"BRL"}}'

# 2) aposta de 80.00
curl -s -X POST localhost:8080/wagering/transactions -H 'Idempotency-Key: provider-a:bet-1' -d '{
  "providerId":"provider-a","externalTransactionId":"bet-1",
  "playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","walletId":"<walletId>",
  "roundId":"round-1","gameId":"fortune-chimp","kind":"BET","money":{"amount":"80.00","currency":"BRL"}}'

# 3) saldo, ledger e reconciliação
curl -s localhost:8080/wallets/<walletId>
curl -s 'localhost:8080/wallets/<walletId>/ledger?limit=50'
curl -s -X POST localhost:8080/wallets/<walletId>/reconciliation

# 4) consulta da transação
curl -s localhost:8080/providers/provider-a/wagering/transactions/bet-1
```

### Replay idempotente

Repita o passo 2 com **a mesma chave e o mesmo corpo**: a resposta traz o mesmo `transactionId` e `"idempotentReplay": true`, e o saldo não muda. Com o mesmo `Idempotency-Key` e outro valor, a resposta é `409 IDEMPOTENCY_CONFLICT`.

### Reversão que chega antes da referência

```sh
# o REFUND chega antes da BET que ele referencia → 202 PENDING_REFERENCE
curl -s -X POST localhost:8080/wagering/transactions -H 'Idempotency-Key: k-refund-1' -d '{
  "providerId":"provider-a","externalTransactionId":"refund-1",
  "playerId":"<playerId>","walletId":"<walletId>","roundId":"round-2","gameId":"g",
  "kind":"REFUND","money":{"amount":"25.00","currency":"BRL"},
  "referenceExternalTransactionId":"bet-2"}'

# a BET chega depois
curl -s -X POST localhost:8080/wagering/transactions -H 'Idempotency-Key: k-bet-2' -d '{
  "providerId":"provider-a","externalTransactionId":"bet-2",
  "playerId":"<playerId>","walletId":"<walletId>","roundId":"round-2","gameId":"g",
  "kind":"BET","money":{"amount":"25.00","currency":"BRL"}}'

# em até um intervalo de varredura o worker conclui o REFUND
curl -s localhost:8080/providers/provider-a/wagering/transactions/refund-1   # status PROCESSED
```

Se a referência nunca chegar, depois de `REFERENCE_MAX_ATTEMPTS` tentativas (ou do `REFERENCE_TTL`) a reversão vira `REJECTED` com `REFERENCE_NOT_FOUND`, sem alterar a carteira.

## Observabilidade

- **Logs:** JSON, um evento por linha, com `correlationId` (gerado quando o cliente não envia `X-Correlation-Id`; devolvido no header da resposta), `transactionId`, `walletId`. Os eventos do Fx (início e parada de componentes) saem no mesmo formato. Credenciais e payloads financeiros completos não são registrados.
- **Health:** `GET /health/live` (processo) e `GET /health/ready` (PostgreSQL e SQS; 503 com a dependência que falhou).
- **Métricas** (`GET /metrics`, formato Prometheus): `http_requests_total{method,route,status}`, `http_request_duration_seconds`, `wallet_reconciliation_divergences_total`, `pending_reference_outcomes_total{outcome}` (`processed`, `rejected`, `retry`, `expired`) e as métricas padrão de Go e do processo. O executável `worker` não expõe HTTP: seus desfechos aparecem nos logs.

## Testes e verificação

Tudo roda em containers (`make` chama o serviço `dev`):

```sh
make test               # unitários            (go test ./...)
make test-race          # com race detector    (go test -race ./...)
make vet                # go vet ./...
make fmt                # gofmt
make tidy               # go mod tidy
make test-integration   # integração com PostgreSQL e LocalStack reais
```

`make test-integration` sobe PostgreSQL e LocalStack, aplica as migrations, cria as filas e roda `go test -race -p 1 -tags integration ./...`. Os pacotes rodam em série (`-p 1`) porque compartilham a mesma base. Os testes de integração usam a build tag **`integration`** e serviços reais, sem mocks.

O que a suíte cobre:

- **Domínio:** `Money` (parsing, limites, overflow, moedas), carteira, ledger, máquina de estados e todas as regras das cinco operações e das reversões.
- **Banco:** constraints, imutabilidade do ledger, atomicidade, unicidades e o índice de reversão única.
- **Concorrência (três instâncias, pools independentes):** duas apostas de 80.00 sobre 100.00 (uma processada, uma rejeitada, saldo 20.00, um débito), a mesma aposta 50 vezes em paralelo (um único débito), carteiras distintas em paralelo, uma carteira bloqueada sem atrasar as demais, `REFUND` e `ROLLBACK` simultâneos da mesma aposta. Ao final, o saldo armazenado é conferido contra o ledger.
- **Idempotência:** rajada de 50 requisições, replay, conflitos, retomada por lease, indisponibilidade do banco.
- **Worker de referências pendentes:** retomada, backoff crescente, expiração por tentativas e por TTL, três instâncias disputando 30 pendências, recuperação de trabalho abandonado.
- **Composição Fx:** grafo de dependências, início, atendimento, parada com porta e pool fechados, ordem servidor → pool, falha de início com banco inacessível, `SIGTERM`.

Para rodar um teste específico:

```sh
docker compose run --rm dev go test ./internal/domain -run TestApplyTwoBetsOfEightyOnHundred -v
```

## Simulando falhas e várias instâncias

**Duas apostas de 80.00 sobre 100.00 ao mesmo tempo, em réplicas diferentes**

```sh
docker compose up -d --build --scale app=3 --wait app worker
A=$(docker compose port --index 1 app 8080 | cut -d: -f2)   # porta da réplica 1
B=$(docker compose port --index 2 app 8080 | cut -d: -f2)   # porta da réplica 2
# abra uma carteira com 100.00 e então dispare as duas apostas em paralelo:
curl -s -X POST localhost:$A/wagering/transactions -H 'Idempotency-Key: k1' -d '<aposta ext-1 de 80.00>' &
curl -s -X POST localhost:$B/wagering/transactions -H 'Idempotency-Key: k2' -d '<aposta ext-2 de 80.00>' &
wait
```

Resultado esperado: uma resposta `200 PROCESSED` e outra `422 REJECTED` com `INSUFFICIENT_FUNDS`; saldo final `20.00`, um único débito no ledger e a reconciliação consistente.

**Banco indisponível**

```sh
docker compose stop postgres
curl -i localhost:8080/health/ready          # 503, "postgres": "error"; /health/live continua 200
curl -i -X POST localhost:8080/wagering/transactions ...   # 503 UNAVAILABLE, nada é gravado
docker compose start postgres
# reenviar a mesma Idempotency-Key funciona normalmente depois que o banco volta
```

**Encerramento gracioso**

```sh
docker stop -t 30 desafio-app-1    # SIGTERM: recusa novas conexões, conclui as em andamento, fecha o pool; sai com código 0
docker stop -t 30 desafio-worker-1 # o laço do worker termina antes do pool; o trabalho não concluído fica reivindicável por outra instância
```

**Configuração inválida**

```sh
docker compose run --rm -e DATABASE_URL= app api   # sai com código 1 e "DATABASE_URL é obrigatória"
```

## Estrutura do repositório

```
cmd/api/                 executável HTTP
cmd/worker/              executável dos workers
internal/domain/         regras de negócio puras (Money, Wallet, ledger, WagerTransaction, Apply)
internal/app/            casos de uso e portas (carteiras, transações, idempotência, worker de referências)
internal/adapters/       http, postgres (pgx, SQL explícito) e sqs
internal/platform/       config, logging, métricas, ids (UUIDv7) e relógio
internal/bootstrap/      única camada que conhece o Uber Fx (módulos e composições)
migrations/              migrations versionadas (up/down)
deploy/localstack/       provisionamento idempotente das filas
```

`internal/domain` e `internal/app` não importam o Fx, nem HTTP, SQS ou PostgreSQL (verificado por teste).

## Solução de problemas

| Sintoma | Causa e solução |
| --- | --- |
| `port is already allocated` (5432, 4566, 8080) | Outro serviço usa a porta. Pare-o, ou ajuste o mapeamento em `docker-compose.yml`. |
| A API não responde em `8080` com várias réplicas | O Compose escolhe portas livres da faixa `8080–8089`; use `docker compose port --index N app 8080`. |
| `/health/ready` devolve 503 no início | PostgreSQL ou LocalStack ainda subindo; aguarde o healthcheck (`docker compose ps`). |
| `Idempotency-Key` → 400 | O header é obrigatório e deve ter de 1 a 255 caracteres ASCII imprimíveis, sem espaços. |
| Quero começar do zero | `docker compose down -v` apaga os volumes (banco e cache de módulos). |
| Os testes de integração falham com `lookup postgres` | Os serviços não estão de pé; use `make test-integration`, que sobe tudo antes. |

## O que ainda não existe

O desafio exige, e este projeto **ainda não implementa**:

- **Autenticação e autorização** (OAuth 2.0/OIDC com IdP externo, por exemplo Keycloak): hoje todas as rotas são públicas, e não há provisionamento de IdP nem identidades de teste.
- **Consumidor SQS, inbox e outbox:** nada consome a fila de entrada; os eventos de integração (`WagerTransactionProcessed`, `WagerTransactionRejected`, `WalletBalanceChanged`, `WagerTransactionPendingReference`) ainda não são publicados, e a DLQ ainda não recebe mensagens.
- **Testes de recuperação com processos reais** (interrupção entre commit e remoção da mensagem, dois publishers disputando a outbox, reinício completo).
- O executável `worker` não expõe endpoint HTTP de métricas ou health.

Os detalhes, as decisões tomadas e as limitações conhecidas estão em [ARCHITECTURE.md](ARCHITECTURE.md).
