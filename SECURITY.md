# Security policy

## Supported versions

Only the latest tagged source release is supported. Compatibility with a
specific official ChatGPT build is listed in `docs/COMPATIBILITY.md`.

## Reporting a vulnerability

Do not open a public issue for a suspected credential leak, signing bypass,
arbitrary code execution path, or control-server authentication flaw. Use the
repository's **Security → Report a vulnerability** form. If that form is not
available, open a private draft security advisory from the Security tab and
invite the repository owner. Include:

- the project version and exact commit;
- the official ChatGPT build used as input;
- reproduction steps and expected impact; and
- whether credentials or other private data may have been exposed.

Do not include real access tokens, device codes, account identifiers, or
private conversation content. Revoke affected credentials before sharing a
redacted reproduction.

## Scope

Reports about OpenAI's unmodified app or service should go to OpenAI. This
project's scope is the patcher, multiplexer, injected UI, isolated local state,
and independent signing flow.
