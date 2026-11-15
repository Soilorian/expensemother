# Expense Mother

Go backend and Telegram group bot for shared expenses, draft orders, receipt items, and repayments. Amounts are JSON numbers in whole Rials. Persian and English receipt OCR runs locally through Tesseract.

## Run with Docker

For GitHub testing, container publishing and server deployment, see the [CI/CD setup guide](docs/deployment.md). The production stack is `compose.production.yaml`; it runs the API and Telegram bot together with PostgreSQL.

1. Copy `.env.example` to `.env`.
2. Set a random `POSTGRES_PASSWORD` and an `AUTH_SECRET` of at least 32 random bytes. Use a URL-safe database password in this Compose configuration.
3. Set `TELEGRAM_BOT_TOKEN` from BotFather to enable the bot, or leave it blank to run the API only.
4. Start:

```sh
docker compose up --build
```

The API is at `http://localhost:7008`; PostgreSQL is exposed locally on port 5433. Startup applies embedded SQL migrations. Receipt files and database data use persistent Docker volumes. The image includes Tesseract and `fas`/`eng` language data. A single app process handles HTTP and Telegram long polling.

`GET /healthz` checks the process; `GET /readyz` checks database access. Business operations are documented in [webExpenseMotherV1.yml](webExpenseMotherV1.yml).

## Run Go directly

Requirements: Go 1.24 or later, PostgreSQL 17 or later, and Tesseract 5 with Persian (`fas`) and English (`eng`) data. Use a currently supported patched Go toolchain for deployment.

```sh
go mod download
go test ./...
go run ./cmd/server
```

Export the variables in `.env` before running Go. On Windows the helper loads `.env` without executing it:

```powershell
Copy-Item .env.example .env
# Edit .env, then:
./scripts/run.ps1
```

`DATABASE_URL` points to your PostgreSQL database; `AUTH_SECRET` is mandatory. `TESSERACT_BIN` can specify a custom executable path. If Tesseract is absent or fails, images are retained and the receipt flow offers manual entry rather than creating a guessed final bill.

Build a standalone executable with `go build -o bin/expensemother ./cmd/server` (use `.exe` on Windows). The SQL migration is embedded; the receipt directory must be writable and persistent.

## Telegram setup

1. Create a bot in BotFather and put its token in `.env`.
2. Start one app instance. Long polling must not coexist with a configured webhook; remove an old webhook before starting this bot.
3. Add the bot as a Telegram group administrator, then run `/setup`. To link an existing application group, use `/setup <group-id>` as both its application administrator and a Telegram chat administrator. Administrator status enables reliable membership checks. Application roles are managed separately afterward.
4. Members run `/join`, or participate through the draft buttons.

Commands:

- `/expense Dinner 1200000`: create a draft. A description without an amount is also allowed.
- `/orders`: browse drafts and finalized expenses using buttons.
- `/receipt`: reply with a receipt photo or image file (JPEG/PNG/WebP, up to 10 MiB).
- `/mytotal`, `/balances`: personal spending, payments, outstanding debt and receivables; draft previews are separate.
- `/guest Name`: an administrator adds a named participant without login credentials.
- `/cancel`: cancel an active prompt while preserving saved drafts.
- `/help`: display commands and guidance in English/Persian.

Draft cards offer Join, Leave, Weight, Items, Edit and Finalize. Quantity items allocate purchased quantities; shared items divide a line by positive claim weights. Finalization checks participants, exact totals and unassigned items. Reply to the specific bot prompt so messages for different expenses do not mix.

The Edit prompt accepts one change at a time:

```text
name Dinner
amount 1200000
payer 1
participant 2
split WEIGHT
item Pizza | 2 | 800000 | QUANTITY
item Service | 1 | 50000 | SHARED
line 1 | Pizza | 3 | 1200000 | QUANTITY
drop 2
adjust TAX 120000
adjust DISCOUNT 50000
clearadjust
type INCOME
receiver 1
```

Member numbers are shown by the prompt. Use `split ITEM` for item allocation; switching to EQUAL/WEIGHT explicitly removes the item breakdown. Review OCR suggestions using Edit, join participants, assign items, correct the total, and then Finalize. Toman detection requires manual confirmation/conversion into Rials; the bot does not silently treat Toman as Rials. OCR is heuristic and does not guarantee receipt accuracy.

Final expenses offer full/partial payment with confirmation, recording payment received from a member, repayment undo, and reopening. Payments are records of money transferred outside the app. Expenses with active repayments must have those payments reversed before editing/cancelling. Only the actor authorized by the domain can confirm a sensitive action; another group member cannot press someone else's confirmation.

## Passwordless accounts and future clients

Joining through a verified Telegram update creates a normal account with no password. Browser sign-in and Mini App authentication resolve to that same internal UUID:

- `POST /v1/auth/telegram/challenge` + `/login`: Telegram Login ID-token verification using RS256, Telegram's JWKS, issuer/audience/expiry and single-use nonce.
- `POST /v1/auth/telegram/mini-app`: verify raw signed `initData`, freshness and replay protection.
- `POST /v1/auth/telegram/link`: authenticated existing-account linking with fresh proof; conflicting identity ownership returns 409.

Set `TELEGRAM_CLIENT_ID` and configure allowed website origins in BotFather for browser login. This implementation expects Telegram Login's RS256 setting and a profile scope that supplies signed `id`. `sub` is stored separately and is never assumed to be the bot user ID. No provider token is accepted as an application bearer token.

`ALLOWED_ORIGINS` is a comma-separated browser allowlist. `WEB_APP_URL` optionally adds an “Open app” URL to the bot help; it must point to your separately hosted web UI. This delivery contains the API and Telegram chat UI, not the future PWA/Android frontend. The Mini App authentication endpoint is ready for that UI.

## Testing

```sh
go test ./...
go vet ./...
# Optional, with a C compiler supported by your Go toolchain:
go test -race ./...
```

Set `TEST_DATABASE_URL` to run real PostgreSQL integration tests. They create a uniquely named temporary schema, exercise migrations/persistence/concurrent identity and payments, then drop only that schema. Without the variable this integration test explicitly skips; domain, HTTP, OCR-parser, authentication, and mocked Telegram tests still run.

```powershell
$env:TEST_DATABASE_URL='postgres://expense:YOUR_PASSWORD@localhost:5433/expensemother?sslmode=disable'
go test ./...
```

[Implementation and validation notes](docs/implementation.md) distinguish tested behavior from external setup still needed.

## Structure and persistence

- `internal/{identity,groups,expenses,receipts}/domain`: domain records and business rules.
- `internal/application`: shared use cases coordinating domain permissions and transactions.
- `internal/adapters`: HTTP, Telegram, PostgreSQL, authentication, private files and OCR integrations.
- `internal/adapters/postgres/migrations`: embedded SQL migrations.

The first SQL adapter stores each aggregate's nested members/shares/items as JSONB, with relational keys and indexes for identities, groups, parent repayments and receipts. This keeps aggregate writes atomic without a table or generic repository for every nested value. Application checks enforce referenced membership; no user-deletion operation exists. Lists and balances are calculated on demand for modest initial datasets. Normalize/index additional read models when actual usage warrants it.

General idempotency, queues, distributed deployment, refresh-token/device management, exchange rates, bank integration, guest-account merging and debt optimization remain deferred as agreed. Prompt/confirmation state is in memory; saved drafts and financial records survive restarts. A transport failure can leave an expense saved without a reply, so check `/orders` before retrying creation.

## Backup and operation

Back up PostgreSQL (`pg_dump`) and the receipt volume together, preferably while writes are paused, and test a restore into a separate database. Keep `.env`, receipt files, test databases, local tool downloads and compiled executables out of version control. Deploy behind HTTPS for remote access; the supplied Compose ports bind to localhost. Run only one polling instance per bot token.
