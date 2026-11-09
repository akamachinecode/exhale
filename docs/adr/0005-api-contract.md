# 0005. API contract: huma v2, code-first OpenAPI 3.1

- Status: Accepted
- Date: 2026-11-05

## Context — what it has to do

- About 40 operations for the web, with types and validation defined **once**.
- SSE for live updates and file upload for logos.
- A **public, documented REST API** (scripts, Home Assistant, iOS Shortcuts) as a product feature.
- The bots do not use it: they call ⑦ in-process.

## Options

huma v2 (code-first), oapi-codegen (spec-first YAML), Connect-RPC (protobuf, not REST-friendly for third parties), GraphQL (overkill), no contract (drift).

## Decision

**huma v2 on stdlib `net/http`.** Go structs and tags generate the OpenAPI 3.1 document, request validation, interactive docs at `/docs`, and the source for the generated TypeScript client.

## Consequences — cost we accept

huma has one main maintainer. It sits on the stdlib router and handlers are plain Go functions, so if it dies, the generated spec allows migrating to oapi-codegen without rewriting logic.

## Revisit when

huma becomes unmaintained, or we need complex bidirectional streaming.
