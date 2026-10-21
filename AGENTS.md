# Agent instructions

Read [README.md](README.md) for the client interface and local checks.
Read the [SDK maintenance guide](https://github.com/Sandfi-sh/openapi/blob/main/MAINTAINING.md)
for cross-repository work and the
[release guide](https://github.com/Sandfi-sh/openapi/blob/main/RELEASING.md)
for version tags and module-proxy verification.

- Check the working tree, remote revision, and unpublished commits first.
- Change API methods and models through `sandfi-sh/openapi`, then regenerate.
- Use `.sdk-generated.json` to identify generated files, including module files.
- Maintain client helpers in `helpers.go` and their checks in `helpers_test.go`.
- Preserve nullable types, enum prefixes, context cancellation, and streaming.
- Keep the module at the repository root with path `github.com/sandfi-sh/sandfish-go`.
- Run `go test ./...` and `go vet ./...` for code changes.
- Verify an exact release tag through the module proxy and an API call.
- Check links, references, commands, and `git diff --check` for prose changes.
- Keep credentials out of Git, documentation, and tool output.
- Update the owning guide and report local, published, and verified state.

Use short, direct sentences, active voice, and consistent technical terms.
