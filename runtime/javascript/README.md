# agent-compose JavaScript Runtime

`agent-compose-runtime-js` is the guest-side runtime used by agent-compose agent sessions. It exposes the compatible CLI entrypoint:

```sh
agent-compose-runtime-js prompt \
  --provider <codex|claude|gemini> \
  --message-file <path> \
  --output-schema-file <path> \
  --state-root <path> \
  --workspace <path> \
  --home <path>
```

Successful runs write a single structured result line to stdout with the `__AGENT_RESULT__` prefix. Human-readable agent transcript output is written to stderr.

`--output-schema-file` is optional. When set, the file must contain a JSON Schema object. The runtime passes it to the provider's native structured-output mechanism where supported. Codex and Claude support schema-based output; Gemini currently rejects schema requests until a native CLI schema flag is wired.

## Development

```sh
npm install
npm run build
npm test
```

The TypeScript source lives in `src/`:

- `cli.ts`: commander-based CLI.
- `prompt.ts`: command orchestration and default path resolution.
- `runners/`: provider adapters for Codex, Claude, and Gemini.
- `mpi.ts`: MPI catalog discovery and context formatting.
- `session-state.ts`: provider resume state persistence.
