# Contributing to kardbrd-agent

## Quick start

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
uv sync --dev
uv run pytest              # run tests
uv run pre-commit run --all-files  # lint and format
```

## Development workflow

1. Create a branch for your change
2. Write code following the [code conventions](https://kardbrd.github.io/kardbrd-agent/contributing/conventions/)
3. Add tests for new functionality
4. Ensure all tests pass: `uv run pytest`
5. Ensure linting passes: `uv run pre-commit run --all-files`
6. Submit a pull request

## Documentation

Full documentation is available at [kardbrd.github.io/kardbrd-agent](https://kardbrd.github.io/kardbrd-agent/):

- [Development setup](https://kardbrd.github.io/kardbrd-agent/contributing/development/)
- [Testing guide](https://kardbrd.github.io/kardbrd-agent/contributing/testing/)
- [Code conventions](https://kardbrd.github.io/kardbrd-agent/contributing/conventions/)
- [Architecture overview](https://kardbrd.github.io/kardbrd-agent/architecture/)
