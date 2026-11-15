# AGENTS.md

Guidance for AI agents working in this repository. For *using* the CLI, read `skills/ne-shared/SKILL.md`.

## Layout

| Path | Role |
| --- | --- |
| `main.go` | entry point |
| `cmd/root.go` | global flags, error rendering, exit codes |
| `cmd/call.go` | `send`: token attach/rotation, validation, dry-run, error mapping; parameter assembly |
| `cmd/service.go` | builds `ne-cli <service> <resource> <method>` from the catalog; paging |
| `cmd/api.go`, `cmd/schema.go`, `cmd/auth.go`, `cmd/config.go` | core commands |
| `cmd/shortcuts.go` | `+` shortcuts |
| `internal/registry` | embedded endpoint catalog (`meta.json`), lookup, request validation |
| `internal/client` | form-encoded POST and response decoding |
| `internal/config` | profiles, credentials and tokens on disk |
| `internal/output` | json / pretty / table / ndjson / csv |
| `scripts/gen_meta.py` | regenerates `meta.json` from the official OpenAPI spec |
| `skills/` | Agent Skills shipped with the CLI |

## Rules

- `internal/registry/meta.json` is generated. Never edit it by hand; change `scripts/gen_meta.py` and run `make meta`.
- API commands are data-driven. Do not add a hand-written command for a single endpoint; add a shortcut only when it encodes a multi-step task or a rule that is easy to get wrong.
- Tokens and the client secret must never reach stdout, stderr or `--dry-run` output.
- Every authenticated API call goes through `send` in `cmd/call.go`, so rotated tokens are always saved.
- Success goes to stdout as `{"ok":true,…}`; failure goes to stderr as `{"ok":false,"error":{…}}` built from a `cliError`. Keep the exit codes in `cmd/root.go` stable.
- Endpoints whose effect is hard to undo are classified `high-risk-write` in `scripts/gen_meta.py` and require `--yes`.
- Tests use a fake server (`cmd/cmd_test.go`); they must not call the real API.

## Checks

```bash
make lint && make test
```
