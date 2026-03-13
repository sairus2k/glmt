# Contributing to glmt

Thanks for your interest in contributing to glmt! This guide covers everything you need to get started.

## Prerequisites

- Go 1.26+
- A GitLab instance (self-hosted or gitlab.com) for manual testing
- Docker (for running e2e tests)

## Getting started

```bash
git clone https://github.com/sairus2k/glmt.git
cd glmt
go build -o glmt ./cmd/glmt/
```

## Project structure

```
cmd/glmt/          Entry point
internal/
  auth/            Credential reading (glab CLI config)
  config/          Config file handling (~/.config/glmt/config.toml)
  gitlab/          GitLab API client and interface
  train/           Merge train runner logic
  tui/             Terminal UI (Bubble Tea v2)
e2e/               End-to-end tests (testcontainers + GitLab)
```

## Development workflow

1. Fork and create a feature branch from `main`.
2. Make your changes.
3. Run checks locally before pushing (see below).
4. Open a pull request against `main`.

## Code style

- Run `gofmt` on all files. CI rejects unformatted code.
- Run `go vet ./...` to catch common issues.
- Keep the code simple and minimal — avoid unnecessary abstractions.

## Testing

### Unit tests

```bash
go test ./...
```

Unit tests live alongside the code they test (`*_test.go` files in each package). Tests use the standard `testing` package and [testify](https://github.com/stretchr/testify) for assertions.

### End-to-end tests

E2E tests are guarded by a build tag and use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) to spin up a real GitLab instance:

```bash
go test -tags e2e ./e2e/...
```

These tests are slow (GitLab container startup) and require Docker. They are not part of the default `go test ./...` run.

## CI

Pull requests run three jobs automatically:

1. **lint** — `go vet` + `gofmt` check
2. **test** — `go test ./...`
3. **build** — verifies the binary compiles

All three must pass before merging.

## Submitting changes

- Keep PRs focused — one feature or fix per PR.
- Write clear commit messages describing *what* and *why*.
- Add tests for new functionality.
- Update the README if your change affects user-facing behavior.

## Releases

Releases are handled via [GoReleaser](https://goreleaser.com/) and triggered automatically by pushing a Git tag:

```bash
git tag v0.x.x
git push origin v0.x.x
```

This triggers a GitHub Actions workflow that:

1. Builds binaries for Linux and macOS (amd64/arm64)
2. Creates a GitHub Release with `.tar.gz` archives
3. Updates the [Homebrew tap](https://github.com/sairus2k/homebrew-tap)

The `TAP_GITHUB_TOKEN` repository secret is required for the Homebrew tap update.

## License

By contributing, you agree that your contributions will be licensed under the same license as the project.
