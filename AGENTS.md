# Repository Guidelines

## Project Structure & Module Organization
This repository is a small Go module centered in the repo root. Application entry points and proxy logic live in top-level files such as `main.go`, `http.go`, `socks.go`, `dial.go`, and `config.go`. Proxy dialers live in the `proxy` package. Tests currently live beside the code in `_test.go` files, for example `http_test.go`.

## Build, Test, and Development Commands
- `go build .` builds the `yeager` binary in the current directory.
- `./yeager -listen=socks://127.0.0.1:1080 -proxy=ss://method:password@host:port` runs a local proxy with inline flags.
- `./yeager -c config.json` starts the proxy from a config file; `config.go` documents the supported fields.
- `go test ./...` runs the full test suite. This currently passes and should remain the default verification step.
- `go test -run TestHttpsProxy` runs a focused test when iterating on HTTP/HTTPS behavior.

## Coding Style & Naming Conventions
Follow the existing Go style and keep code explicit. Prefer early returns, small functions, and standard-library APIs unless a dependency is already in `go.mod`. Use `gofmt` on every change and keep package names lowercase and single-word. Avoid stuttering names and clever abstractions; examples in this repo favor concise identifiers like `dialer`, `transport`, and `config`.

## Testing Guidelines
Write table-driven tests in the same package (`package main`) and keep test files named `*_test.go`. Name tests with the standard `TestXxx` pattern, matching the behavior under test, such as `TestHttpProxy`. Add or update tests for new proxy protocols, routing behavior, and config parsing whenever logic changes.

## Go Tooling
Use `gopls` as the first choice for code navigation and investigation. Prefer it over plain text search when you need definitions, references, implementations, or callers for Go symbols. Keep `rg` for broad text search, but use `gopls` when the question is about Go APIs or symbol relationships.

## Commit & Pull Request Guidelines
Recent commits use short, direct subjects such as `clean`, `clean code`, and `naming the proxy server`. Keep commit messages brief, imperative, and focused on one change. Pull requests should explain the behavior change, list the commands used for verification, and note any config or protocol impact. Include example flags or sample config when user-facing behavior changes.

## Configuration & Security Notes
Do not commit real proxy credentials or private endpoints in `config.json` examples. Prefer sanitized placeholders in docs and tests, and keep reproducible examples minimal.
