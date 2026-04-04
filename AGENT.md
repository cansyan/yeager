# Agent Context: Go Engineering

## Project Overview
- **Primary Goal:** Build a lightweight proxy tool.
- **Tech Stack:** Go (Standard Library).

## Engineering Standards (The "Go Way")
### Core Philosophy
- **Simplicity First:** Prioritize explicit, readable, and minimalist code.
- **Happy Path:** Keep it left-aligned. Use early returns to minimize nesting.
- **No Cleverness:** Avoid complex abstractions, deep embedding, or unnecessary concurrency.

### Implementation Details
- **Standard Library:** Default to `stdlib`. Avoid third-party frameworks unless already present.
- **Error Handling:** Use explicit `if err != nil`. No helper functions that hide error flow.
- **Testing:** Table-driven tests in `_test.go` files (same package).
- **Interfaces:** Accept interfaces, return concrete types. Keep interfaces small (1-3 methods).
- **Comments:** **English only.** Document *why*, not *what*.
- **Context:** Pass `ctx context.Context` as the first argument for I/O-bound functions.

### Naming & Structure
- **Conciseness:** Avoid stuttering (e.g., `server.Server`, not `server.HTTPServer`).
- **Package Design:** Single-word, lowercase names. No `util` or `common`.
- **Zero Values:** Design structs so the zero value is useful.

## Operation & Workflow
- **Dependencies:** Check `go.mod` before suggesting new packages.
- **Performance:** Preallocate slices with `make([]T, 0, len)` when size is known. Avoid pointers for small structs.

## Investigation & Debugging
- When asked to debug, first look for logs using the `slog` (or current logging) pattern.
- Check `README.md` for local environment setup instructions.
