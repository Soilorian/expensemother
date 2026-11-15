# Implementation and validation

Delivered 10 September 2026: one Go service with PostgreSQL persistence, all 31 API operations in the OpenAPI contract, and a Telegram group chat UI. Start with the [setup guide](../README.md).

## Implemented behavior

- Identity, groups, expenses and receipts form the domain boundaries. Application use cases enforce membership, edit authority, financial invariants and transaction boundaries. HTTP and Telegram share those use cases.
- Draft creation, participation, equal/weighted/item allocations, adjustments, finalization, cancellation, partial repayments and reversals. Whole-Rial arithmetic uses exact allocation and deterministic remainder distribution.
- Group setup and existing-group linking (`/setup <group-id>`), guests, orders, personal totals, group balances and inline action buttons. Linking requires both Telegram chat administrator and application group administrator authority.
- Password accounts and verified Telegram identities; signed browser login challenges and Mini App authentication resolve to the same account. Guest accounts cannot authenticate.
- Private receipt uploads, local Persian/English Tesseract extraction, editable draft previews and manual entry when extraction fails. Toman detection requires explicit correction to Rials. No extraction finalizes an expense automatically.
- PostgreSQL transactions protect payment limits and identity uniqueness. Nested aggregate values use JSONB with relational identity/group/repayment keys; this is the concrete initial persistence choice in place of the design's fully normalized illustrative tables.

## Validation

The complete Go test suite passes, including real PostgreSQL 17.4 integration tests. Coverage includes exact rounding, incomplete drafts, item claims, partial/duplicate concurrent payments, reversals, historical access, private receipts, identity linking, signed login validation, Mini App replay rejection, HTTP workflows and simulated Telegram conversations. PostgreSQL tests also verify persistence across connection pools and concurrent identity creation. Static analysis and executable compilation pass. The built executable passed live health/readiness checks and eight response-schema validations; all 31 registered API routes match the structurally validated YAML.

Telegram tests use a local fake Bot API. No live bot token was supplied or used. OCR parsing and failed-engine manual fallback are tested; recognition quality still needs validation on representative real receipts with Tesseract and its Persian/English language packs. Docker configuration is supplied, but its image was not built locally because the Docker engine was unavailable. Tests used an isolated local PostgreSQL instance.

## Delivery boundaries

The delivered UI is Telegram chat commands, prompts and inline buttons. The shared PWA/Telegram Mini App frontend and native Android app remain future clients; authentication and API support are present. Configure the real bot token, browser login settings and HTTPS hosting to activate external integrations.

Prompts and confirmation tokens are temporary in-memory state; persisted drafts and repayments survive restarts. This version uses one polling process and calculates lists/balances on demand. General request idempotency, horizontal scaling, queues and additional infrastructure remain deferred as requested. The SQL adapter applies the initial repeatable schema; future schema changes need explicit migration versions.
