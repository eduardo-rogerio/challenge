# Arquitetura

Documento vivo. Cada seção indica se a decisão já foi tomada (**Decidido**) ou ainda está **Pendente** para uma próxima change.

## Estrutura do código — Decidido

```
cmd/api/        binário HTTP (e, no futuro, consumidor SQS)
cmd/worker/     binário dos workers (outbox, referências pendentes) — esqueleto
internal/domain/            regras de negócio; sem HTTP, SQS, pgx ou composição (verificado por teste)
internal/app/               casos de uso e portas (interfaces)
internal/adapters/{http,sqs,postgres}/
internal/platform/{config,logging,metrics,ids,clock}/
internal/bootstrap/         # única camada que conhece o Uber Fx (módulos e composições)
migrations/  deploy/
```

Dependências apontam para dentro: adapters e platform conhecem `app`/`domain`, nunca o contrário.

## Composição e ciclo de vida — Decidido

A composição usa **Uber Fx**, confinado a `internal/bootstrap` e `cmd/*`. Domínio, casos de uso e adaptadores têm construtores simples e não importam o Fx (um teste de dependências impõe isso em `internal/domain` e `internal/app`).

- **Módulos** (`fx.Module`): `database` (pool PostgreSQL), `messaging` (cliente SQS), `observability` (métricas), `application` (store, ids, relógio, serviços de carteira e transação) e `http` (handler e servidor). `bootstrap.API` e `bootstrap.Worker` combinam os módulos de cada executável; `bootstrap.Worker` já executa o worker de referências pendentes; os próximos (outbox, consumidor SQS) se registram nele com `runInBackground`.
- **Grupo `checkers`:** cada dependência registra seu verificador de saúde com `group:"checkers"` e o handler consome o grupo; um componente novo entra no `readiness` sem tocar no servidor.
- **Decorador de idempotência:** `newTransactions` devolve o `TransactionService` já envolvido pelo `IdempotentTransactions`; o serviço sem decorador nunca entra no grafo.
- **Ciclo de vida** (`fx.Lifecycle`): o `OnStart` do pool valida o PostgreSQL (`Ping` dentro de `CHECK_TIMEOUT`) e o do servidor abre o listener sem bloquear; o Fx executa os `OnStop` na ordem inversa dos construtores, então o servidor para (recusando novas conexões e aguardando as em andamento) antes de o pool fechar. Se um `OnStart` falha, o Fx executa o `OnStop` dos que já iniciaram e o processo sai com código 1.
- **Execução** (`bootstrap.Run`): `Start`, espera `SIGINT`/`SIGTERM` (`app.Done()`), `Stop` com prazo `SHUTDOWN_TIMEOUT` (`fx.StopTimeout`). A configuração é carregada e validada **antes** de `fx.New`, então configuração inválida falha sem construir nada.
- **Logs:** os eventos do Fx saem no mesmo `slog` JSON da aplicação (`fxevent.SlogLogger`); início, parada e erros em INFO/ERROR e o detalhe da injeção (cada construtor, com stacktrace) apenas em DEBUG.
- **Verificação:** `fx.ValidateApp` das composições (sem infraestrutura) e testes de integração com PostgreSQL e LocalStack reais: início, atendimento, parada com porta e pool fechados, ordem servidor → pool, falha de início com banco inacessível sem abrir porta, worker e `SIGTERM`.
- **Limitação:** o início valida apenas o PostgreSQL; a validação da fila SQS no início entra com o consumidor (hoje o SQS é verificado só no `readiness`).

## Ambiente local — Decidido

Docker Compose com PostgreSQL 17, LocalStack (SQS), serviços de uso único `migrate` e `queues`, a aplicação e o serviço `dev` (profile `tools`) para executar Go sem instalação local. Migrations rodam em serviço separado para que várias réplicas não disputem a aplicação. Banco: `pgx` com SQL explícito. Métricas ficam na mesma porta da API (aceitável localmente; separar é decisão futura).

## API HTTP — Decidido (parcial)

Cada handler decodifica, valida **só o formato** (UUID, `amount` com `^\d+\.\d{2}$`, moeda de 3 letras maiúsculas, `kind` entre os cinco tipos externos, referência obrigatória em reversões), chama uma porta de `internal/app` (`Wallets`, `Transactions`) e traduz o resultado. Regras de negócio ficam no domínio. Os handlers recebem os serviços reais (`WalletService` e `TransactionService`, este envolvido pelo decorador de idempotência) em `cmd/api/main.go`. `ErrInvalidInput` vira 400; a carteira inexistente no envio, 404.

Estado da transação → HTTP no envio: `PROCESSED` 200, `PENDING`/`PENDING_REFERENCE` 202, `REJECTED` 422 (com `failureCode`), `FAILED` 500; `ErrUnavailable` 503. O README do desafio não fixa esses códigos; esta tabela é uma interpretação.

**Pendentes na API:** autenticação/autorização (as rotas são públicas).

## Domínio — Decidido (parcial)

`internal/domain` é um único pacote puro (só biblioteca padrão; sem I/O, relógio global nem geração de ids: `now` e os ids entram por parâmetro). Os casos de uso (`internal/app`) o ligam ao HTTP e ao banco; `app.Money` e os demais DTOs continuam como strings e são convertidos em `app/convert.go`.

**Money.** `int64` em unidades mínimas, escala fixa de 2, moeda como código de três letras maiúsculas (validado só pelo formato, sem a lista ISO 4217 completa). Intervalo: de `-92233720368547758.08` a `92233720368547758.07`; soma, subtração e negação detectam overflow, e misturar moedas é erro. O parsing externo aceita só `^[0-9]+\.[0-9]{2}$` (sem sinal, notação científica nem arredondamento); negativos só surgem de `Sub`/`Neg`. O valor zero da linguagem (sem moeda) é inválido em todas as operações. Nunca há `float`.

**Wallet e ledger.** `Wallet` é encapsulada (criação com versão 1, reidratação validada). `Credit`/`Debit` constroem o `WalletLedgerEntry` antes de alterar a carteira (falha não deixa estado parcial), incrementam a versão e atualizam `updatedAt`. O lançamento é imutável e valida `balanceAfter = balanceBefore ± money`. A abertura (`OpenWallet`) devolve carteira (versão 1), transação `OPENING` já `PROCESSED` e lançamento de crédito; com saldo zero, só a carteira.

**Máquina de estados.** Transições permitidas: `PENDING` → `PENDING_REFERENCE` | `PROCESSED` | `REJECTED` | `FAILED`; `PENDING_REFERENCE` → `PROCESSED` | `REJECTED` | `FAILED`. `PROCESSED`, `REJECTED` e `FAILED` são terminais. `REJECTED`/`FAILED` exigem `failureCode`; `PROCESSED` registra o saldo observado. Falhas transitórias (infraestrutura indisponível) não são estados: o caso de uso as trata com retry; `FAILED` é reservado a falha permanente.

**Apply.** `Apply(wallet, tx, ref, entryID, now)` é pura: rejeições de negócio levam a transação a `REJECTED` e devolvem `(nil, nil)`; erro só para mau uso. O chamador resolve a referência (`Reference{Tx, Reversed}`).

| `failureCode` | Quando | Natureza |
| --- | --- | --- |
| `INSUFFICIENT_FUNDS` | `BET` sem saldo | definitiva |
| `REVERSAL_INSUFFICIENT_FUNDS` | reversão que debitaria mais que o saldo | definitiva |
| `WALLET_MISMATCH` | carteira, jogador ou moeda da operação divergem da carteira | corrigível |
| `REFERENCE_MISMATCH` | provedor, jogador, carteira, moeda ou rodada divergem da referência | corrigível |
| `REFERENCE_AMOUNT_MISMATCH` | valor da reversão difere do referenciado (reversões parciais não existem) | corrigível |
| `INVALID_REFERENCE_KIND` | `REFUND` de algo que não é `BET`; `ROLLBACK` de `LOSS`/`ROLLBACK` | corrigível |
| `REFERENCE_ALREADY_REVERSED` | a referência já tem uma reversão bem-sucedida | definitiva |
| `REFERENCE_NOT_PROCESSED` | a referência terminou `REJECTED`/`FAILED` | definitiva |
| `REFERENCE_NOT_FOUND` | atribuído pelo chamador ao esgotar tentativas/TTL | definitiva |

**Reversões.** `BET` debita; `WIN` credita (referência opcional a uma `BET` da mesma rodada); `LOSS` não movimenta. `REFUND` credita e só referencia `BET`; `ROLLBACK` desfaz `BET` (credita), `WIN` ou `REFUND` (debitam). **Interpretação adotada:** cada transação referenciada recebe no máximo uma reversão bem-sucedida, de qualquer tipo, então uma `BET` reembolsada não pode sofrer `ROLLBACK` (e vice-versa) e o mesmo débito nunca é devolvido duas vezes; fazer `ROLLBACK` de um `REFUND` é permitido uma vez e não libera a `BET` original (conservador). Referência ausente, `PENDING` ou `PENDING_REFERENCE` deixa a reversão em `PENDING_REFERENCE`, sem movimentar a carteira.

## Persistência e casos de uso — Decidido (parcial)

**Camadas.** `internal/app` define as portas (`Store` com `InTx`/`Read`, e repositórios de carteira, transação e ledger); `internal/adapters/postgres` as implementa com SQL explícito (`pgx`). Os serviços (`WalletService`, `TransactionService`) só orquestram: convertem DTOs, chamam o domínio e gravam. Ids são UUIDv7 (`platform/ids`) e o relógio é UTC com precisão de microssegundos (`platform/clock`); ambos entram por parâmetro. `Money` é gravado como `BIGINT` em unidades mínimas, com a moeda ao lado de cada valor (`domain.Money.Minor()` e `NewMoneyFromMinor`).

**Schema (migration `0003`).** As invariantes são impostas pelo banco, independentemente do código:
- `wallets`: `balance >= 0`, `version >= 1`, `UNIQUE (player_id, currency)`.
- `wager_transactions`: `CHECK`s de coerência estado × campos (`failure_code` ⇔ `REJECTED`/`FAILED`, `result_balance` ⇔ `PROCESSED`, `OPENING` sem metadados externos, `LOSS` com zero e os demais positivos), `UNIQUE (provider_id, external_transaction_id)`, uma `OPENING` por carteira e um índice parcial único que permite **uma única reversão `PROCESSED` por transação referenciada**.
- `wallet_ledger_entries`: valor positivo, saldos não negativos, aritmética por direção, `UNIQUE (wallet_id, transaction_id)`, e triggers que rejeitam `UPDATE`, `DELETE` e `TRUNCATE` (append-only). A coluna `seq` (identity) dá a ordem estável da paginação.
- O saldo da carteira não é derivado do ledger por trigger; a consistência é conferida pela reconciliação.

**Concorrência.** Cada operação é uma transação `READ COMMITTED` com `lock_timeout` de 5 s e começa por `SELECT … FOR UPDATE` da carteira: operações da mesma carteira são serializadas e carteiras diferentes não se bloqueiam (sem lock global). A atualização ainda confere `version` como proteção extra contra lost update. Escolhido o lock pessimista por ser simples, correto sob disputa e cobrir saldo, ledger e estados na mesma decisão; o custo é o throughput de uma única carteira muito disputada. Como uma operação toca uma só carteira, não há ordem de locks a coordenar. Antes de aplicar, o horário usado é `max(relógio, updatedAt da carteira)`, para tolerar relógios diferentes entre instâncias.

**Fluxo de `Submit`** (uma transação SQL): trava a carteira → procura a operação por `(provedor, id externo)` (mesma chave e hash → replay com o saldo original; outro → `ErrIdempotencyConflict`) → cria a transação, resolve a referência e chama `domain.Apply` → grava, no mesmo commit, saldo/versão, a transação no estado final e o lançamento. Rejeições são gravadas (auditáveis). Operações sem dependência concluem de forma síncrona, sem commit intermediário de aceite. O decorador de idempotência continua na frente; o passo de replay e os índices únicos garantem a unicidade também entre entradas que usem chaves diferentes.

**Leituras.** `Read` usa `REPEATABLE READ` somente leitura. O ledger pagina por `seq` com cursor opaco (`base64url("v1:<seq>")`), buscando `limit + 1` para saber se há próxima página. A reconciliação soma créditos e débitos do ledger (incluindo a abertura) e compara com o saldo na mesma visão; divergência vira log e a métrica `wallet_reconciliation_divergences_total`, sem correção.

**Indisponibilidade.** Erros de conexão, recursos, desligamento, `lock_timeout` (`55P03`), deadlock e serialização viram `ErrUnavailable` (503) em todos os pontos de acesso, inclusive no store de idempotência; nada é gravado e a mesma chave pode ser reenviada.

**Limitações.** as "três instâncias" dos testes são três serviços com pools independentes no mesmo processo (testes com processos reais e interrupções ficam para a change de recuperação); os triggers do ledger não impedem um superusuário de desabilitá-los (em produção a aplicação deve usar um papel sem privilégios de DDL); o `seq` do ledger pode ter lacunas após rollbacks.

## Referências pendentes — Decidido

Uma reversão que chega antes da transação que referencia é gravada como `PENDING_REFERENCE` (HTTP 202) e retomada pelo **worker de referências pendentes** (`bootstrap.Worker`, executável `worker`; serviço `worker` no Compose, escalável).

- **Agendamento durável:** `reference_attempts` e `next_attempt_at` em `wager_transactions` (migration `0004`); `next_attempt_at` existe se, e somente se, a transação está `PENDING_REFERENCE` (CHECK), com índice parcial para a busca. Nada vive só em memória: reiniciar ou trocar a instância continua do contador e do agendamento persistidos.
- **Reivindicação com lease:** `Claim` seleciona as pendências vencidas com `FOR UPDATE SKIP LOCKED` e já empurra o `next_attempt_at` para `agora + REFERENCE_CLAIM_LEASE`, devolvendo um lote exclusivo sem manter locks durante o processamento. Instância que cai deixa as pendências reivindicáveis de novo quando a lease vence.
- **Processamento de cada item** (uma transação SQL): trava a carteira (mesma ordem do envio), confirma que a transação ainda está pendente e reaplica `domain.Apply` com a mesma resolução de referência do `Submit`. O worker não tem regra financeira própria.
- **Retry:** sem sucesso, `attempts + 1` e próxima tentativa em `min(base × 2^(attempts−1), máx)` com jitter de ±20% (teto aplicado depois do jitter). Padrões: 10 tentativas, TTL de 1 h, base 5 s, máximo 5 min, varredura de 1 s, lease 30 s, lote 20 (`REFERENCE_*`).
- **Comportamento conforme a referência:** processada → a reversão é concluída (ou rejeitada, por exemplo `REVERSAL_INSUFFICIENT_FUNDS`); `REJECTED`/`FAILED` → `REJECTED` imediato com `REFERENCE_NOT_PROCESSED`; ausente ou ainda pendente → nova tentativa. **Esgotamento** (máximo de tentativas ou TTL desde a criação, o que vier primeiro): `REJECTED` com `REFERENCE_NOT_FOUND` se a referência não existe, ou `REFERENCE_NOT_PROCESSED` se existe mas nunca concluiu. O TTL é um prazo absoluto: pendências antigas expiram no primeiro ciclo depois de uma indisponibilidade longa do worker.
- **Replay:** como a primeira resposta foi `PENDING_REFERENCE`, o decorador de idempotência reavalia replays de resultados pendentes pelo estado atual da transação (que pode já estar `PROCESSED` ou `REJECTED`); resultados terminais continuam devolvidos como persistidos.
- **Execução e parada:** laço em segundo plano (`runInBackground`): o `OnStart` o dispara; o `OnStop` cancela e aguarda até 90% do prazo de parada (o Fx não executa os hooks seguintes depois que o contexto de parada expira, então 10% ficam reservados para fechar o pool), registrando o término. O laço para antes do pool.
- **Observabilidade:** log JSON por desfecho (`processed`, `rejected`, `retry`, `expired`, com `transactionId` e `walletId`) e o contador `pending_reference_outcomes_total{outcome}`.
- **Relógio:** a seleção das vencidas usa o relógio injetado (não o do banco), o que permite testar sem esperar; entre instâncias, um pequeno desvio apenas antecipa ou atrasa uma tentativa.
- **Limitações:** a rejeição por expiração ainda não publica evento (depende do outbox); o executável de worker não expõe HTTP, então a métrica só é visível em teste e os desfechos nos logs; encadeamentos de pendências (uma reversão cuja referência é outra pendência) resolvem em ordem à medida que as referências concluem.

## Idempotência — Decidido (parcial)

Implementada como decorador (`app.IdempotentTransactions`) sobre `app.Transactions`, persistida na tabela `idempotency_keys` (migration `0002`).

- **Hash:** SHA-256 (hex) de JSON canônico (chaves ordenadas) com `providerId`, `externalTransactionId`, `playerId`, `walletId`, `roundId`, `gameId`, `kind`, `money` e, quando presente, `referenceExternalTransactionId`. Não entram a chave, o correlation id nem outros metadados de transporte. Como a validação de formato é estrita (uma única forma aceita para `amount`), não há normalização adicional.
- **Unicidade no banco:** `PRIMARY KEY (key)` e `UNIQUE (provider_id, external_transaction_id)`; `CHECK` garante que `response` existe se, e somente se, o estado é `COMPLETED`.
- **Fluxo:** `Begin` insere a chave como `IN_PROGRESS` (`ON CONFLICT DO NOTHING`); se já existia, lê a linha com `FOR UPDATE` e decide: hash diferente → `IDEMPOTENCY_CONFLICT`; `COMPLETED` → replay do resultado salvo; `IN_PROGRESS` dentro da lease → `REQUEST_IN_PROGRESS`; lease vencida → retomada. Sem linha para a chave, o par provedor/id externo pertence a outra chave → `IDEMPOTENCY_CONFLICT`. Erro do caso de uso libera a chave (`Release`); qualquer resultado, inclusive `REJECTED`, é persistido (`Complete`).
- **Lease:** `IDEMPOTENCY_LEASE` (padrão 30s), medida com o relógio do banco. Deve exceder o tempo máximo de execução do caso de uso.
- **Limitações:** o domínio futuro precisa ser seguro contra reexecução por conta própria (unicidade por operação externa), porque uma retomada pode coincidir com uma execução lenta ou um `Complete` pode falhar depois de a operação ter efeito; não há TTL/limpeza de chaves; a unicidade é global, não por identidade autenticada.
- **Pendente:** ligar `data.idempotencyKey` do SQS ao mesmo decorador (change do consumidor).

## Observabilidade — Decidido (parcial)

Logs JSON (`slog`) com `correlationId` por requisição; métricas Prometheus de requisições HTTP; health checks `live` e `ready`. Métricas de negócio (DLQ, outbox, reconciliação etc.) são Pendentes.

## Pendentes (próximas changes)

- **Inbox/outbox**, consumidor SQS, `MessageGroupId`/`MessageDeduplicationId`, DLQ e limites de tentativas.
- **Autenticação e autorização** (OIDC/Keycloak) — obrigatórias no desafio; hoje todas as rotas são públicas.

## Limitações atuais

Sem autenticação; sem consumidor SQS, inbox, outbox nem eventos; o executável `worker` só executa o worker de referências pendentes e não expõe HTTP; 
