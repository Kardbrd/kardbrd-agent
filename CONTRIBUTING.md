# Contributing to kardbrd-agent

## Quick start

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
go test ./...
go vet ./...
pre-commit run --all-files
```

## Development workflow

1. Create a branch for your change
2. Write code following the [code conventions](https://kardbrd.github.io/kardbrd-agent/contributing/conventions/)
3. Add tests for new functionality
4. Ensure all tests pass: `go test ./...`
5. Ensure static analysis passes: `go vet ./...`
6. Ensure linting passes: `pre-commit run --all-files`
7. Submit a pull request

## Documentation

Full documentation is available at [kardbrd.github.io/kardbrd-agent](https://kardbrd.github.io/kardbrd-agent/):

- [Development setup](https://kardbrd.github.io/kardbrd-agent/contributing/development/)
- [Testing guide](https://kardbrd.github.io/kardbrd-agent/contributing/testing/)
- [Code conventions](https://kardbrd.github.io/kardbrd-agent/contributing/conventions/)
- [Architecture overview](https://kardbrd.github.io/kardbrd-agent/architecture/)
