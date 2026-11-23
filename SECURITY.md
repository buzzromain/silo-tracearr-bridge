# Security policy

## Supported versions

Security fixes are applied to the latest published release. Users should upgrade
to the latest tagged version before reporting an issue that may already be
fixed.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:

```text
https://github.com/buzzromain/silo-tracearr-bridge/security/advisories/new
```

Do not open a public issue for suspected vulnerabilities.

Include:

- Affected version.
- Deployment mode and relevant configuration with secrets removed.
- Reproduction steps.
- Security impact.
- Any suggested mitigation.

Never include Silo API keys, bridge API keys, private media history, client IP
addresses, or production logs containing personal data.

## Operational security

- Use a dedicated, high-entropy Tracearr bridge key.
- Keep the Silo administrator key separate from the bridge key.
- Do not expose the bridge listener publicly without TLS and appropriate network
  access controls.
- Prefer a shared private Docker network over publishing port `8098`.
- Rotate both keys after accidental disclosure.
