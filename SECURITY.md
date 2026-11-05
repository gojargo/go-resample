# Security Policy

## Supported versions

Security fixes are made on the latest `v1.x` release. Please upgrade to the
most recent version before reporting.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/gojargo/go-resample/security/advisories/new)
("Security" tab → "Report a vulnerability"). Include a description of the
issue, the affected version, and a minimal reproducer if possible.

You should receive an acknowledgement within a few days. Once a fix is
available, a GitHub Security Advisory will be published crediting the reporter
(unless you prefer to remain anonymous).

## Verifying releases

Go module downloads are already authenticated by the Go checksum database
(sum.golang.org). In addition, every GitHub release from v1.1.0 on carries a
`checksums.txt` signed keylessly with [Sigstore cosign](https://docs.sigstore.dev/),
and build provenance recorded by GitHub.

Verify the signature, then the archive against the checksums:

```sh
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github\.com/gojargo/go-resample/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

Verify the build provenance of the source archive (GitHub CLI 2.49 or later):

```sh
gh attestation verify go-resample_1.1.0_source.tar.gz --repo gojargo/go-resample
```
