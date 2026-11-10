-- +goose Up
-- ADR 0006: sign-in with an OpenID Connect provider. A member is linked to
-- one identity, the pair (issuer, subject) of the provider's ID token. The
-- email is only used to find the member the first time.
ALTER TABLE members ADD COLUMN oidc_issuer TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN oidc_subject TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX members_oidc ON members (oidc_issuer, oidc_subject) WHERE oidc_subject <> '';

-- +goose Down
DROP INDEX members_oidc;
ALTER TABLE members DROP COLUMN oidc_subject;
ALTER TABLE members DROP COLUMN oidc_issuer;
