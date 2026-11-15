# API contract validation

Date: 10 September 2026

Scope: [webExpenseMotherV1.yml](../webExpenseMotherV1.yml), contract revision 1.1.0. The backend is now implemented; see [implementation validation](implementation.md).

- OpenAPI 3.0.3 structural validation: **passed**, using openapi-spec-validator 0.9.0.
- All local schema/parameter/response references resolve.
- All 31 operations across 24 paths have unique operation IDs and matching required path parameters.
- All 17 original method/path pairs were retained during contract generation.
- 26 positive/negative schema examples passed, using openapi-schema-validator 0.9.0.
- Examples cover incomplete drafts, final expense requirements, numeric whole-Rial money, free-form currency, protected audit/paid fields, claims, payment bodies, Telegram proof payloads, and Persian/English OCR language choices.
- The endpoint inventory in the design document was generated from the validated YAML.

These checks validate contract structure and payload shapes. They do not verify signatures, authorization, identity linking, amount reconciliation, payment concurrency, OCR accuracy, or a running service; those require implementation and behavioral tests. Financial domain rules described in operation/schema descriptions remain service obligations even where OpenAPI cannot enforce them.

Temporary Python validation tools are separate from the Go application's dependencies. The implementation additionally accepts `creditorId` in payment requests to disambiguate multi-beneficiary INCOME repayments; the contract includes this field. Passwords are limited to 72 UTF-8 bytes by the password hashing adapter.
