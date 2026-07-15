# Security policy

## Supported versions

Until stable releases are published, only the current default branch is
supported. After versioned releases begin, security fixes should be applied to
the latest release line.

## Reporting a vulnerability

Use the repository's private GitHub security advisory reporting mechanism when
available. Do not include exploitable details, private location data, tokens,
or credentials in a public issue.

Include:

- Affected version, image tag, or commit.
- Reproduction steps and expected impact.
- Whether the issue requires a malicious pack, unauthenticated HTTP access, or
  a particular deployment configuration.
- Any suggested mitigation.

## Deployment responsibility

The service intentionally does not implement TLS, authentication, CORS, or
rate limiting. Production operators must provide those controls at the network
edge and mount only trusted, finalized SQLite packs read-only.
