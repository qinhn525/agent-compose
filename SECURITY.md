# Security Policy

agent-compose is an experimental project. Please review its security model
before exposing it outside a local development environment.

## Reporting Vulnerabilities

Please report suspected vulnerabilities privately. Do not open a public issue
with exploit details, credentials, tokens, or private infrastructure data.

Use a private GitHub security advisory for this repository:
https://github.com/chaitin/agent-compose/security/advisories/new

Include:

- Affected version or commit.
- Configuration and runtime driver in use.
- Reproduction steps.
- Impact assessment.
- Any logs or traces with secrets redacted.

## Supported Versions

The public release is a preview. Security fixes are expected to target the main
development branch until versioned release support is documented.

## Deployment Guidance

- Keep unauthenticated deployments bound to loopback only.
- Set `AUTH_PASSWORD` and a stable, high-entropy `AUTH_SECRET` when exposing the
  daemon to other users or systems.
- Terminate HTTPS before the daemon in production-like deployments.
- Treat `HTTP_LISTEN=0.0.0.0:7410` as unsafe unless authentication and network
  controls are in place.
- Do not expose guest Jupyter ports directly. Use the agent-compose proxy.
- Treat workspace uploads, Git credentials, environment variables, webhook
  tokens, and LLM API keys as secrets.
- Review runtime driver network behavior before running untrusted workloads.

## Runtime Isolation

agent-compose can run guest workloads with Docker, BoxLite, or Microsandbox.
Isolation properties vary by driver and host configuration. Do not assume a
driver is suitable for hostile code without a separate threat model and runtime
hardening review.

## Webhooks

Webhook endpoints intentionally do not rely on browser UI session cookies.
Configure webhook source tokens or signatures, keep body limits enabled, and
rotate tokens when they may have been exposed.
