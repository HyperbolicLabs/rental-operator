# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability) (the "Report a vulnerability" button on this repository's Security tab). Do not open a public issue.

Please include the affected version, a description of the issue and its impact, and steps to reproduce. Never include a real API key in a report.

We aim to acknowledge reports within 3 business days and to agree on a disclosure date with you. Fixes are released as patch versions and announced in a GitHub Security Advisory.

## Supported versions

Before 1.0, only the latest minor release receives security fixes.

## Handling API keys

The operator reads your Hyperbolic API key from a Kubernetes Secret you control, sends it only to the endpoint configured on the `ProviderConfig` over HTTPS, and never logs it or writes it to a resource's status. If you believe a key was exposed, revoke it in the [Hyperbolic dashboard](https://app.hyperbolic.ai) immediately and update the Secret.
