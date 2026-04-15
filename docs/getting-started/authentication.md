# Authentication

kardbrd-agent requires authentication with both **kardbrd** (for board access) and your **LLM provider** (for AI execution).

## kardbrd authentication

Get your bot token from the kardbrd board settings:

1. Open your board → **Settings** → **Bots**
2. Create a bot or copy the existing bot token
3. Set the environment variable:

```bash
export KARDBRD_TOKEN=<bot-token>
```

## LLM provider authentication

### Claude CLI (default executor)

Claude CLI requires an Anthropic API key:

1. Get an API key from [console.anthropic.com](https://console.anthropic.com/)
2. Set the environment variable:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

3. Verify authentication:

```bash
claude auth status
```

!!! note "Subscription required"
    Claude CLI requires an active Anthropic API subscription. If the API key expires or is revoked, kardbrd-agent posts an error comment on the card with re-authentication instructions.

### Goose (multi-provider executor)

Goose supports 20+ LLM providers. Set your provider and its API key:

=== "Anthropic"

    ```bash
    export GOOSE_PROVIDER=anthropic
    export ANTHROPIC_API_KEY=sk-ant-...
    ```

=== "OpenAI"

    ```bash
    export GOOSE_PROVIDER=openai
    export OPENAI_API_KEY=sk-...
    ```

=== "Google Gemini"

    ```bash
    export GOOSE_PROVIDER=google
    export GOOGLE_API_KEY=...
    ```

=== "Ollama (local)"

    ```bash
    export GOOSE_PROVIDER=ollama
    # No API key needed for local models
    ```

=== "OpenRouter"

    ```bash
    export GOOSE_PROVIDER=openrouter
    export OPENROUTER_API_KEY=...
    ```

=== "AWS Bedrock"

    ```bash
    export GOOSE_PROVIDER=bedrock
    export AWS_ACCESS_KEY_ID=...
    export AWS_SECRET_ACCESS_KEY=...
    export AWS_REGION=us-east-1
    ```

!!! tip
    Run `goose configure` to interactively set up your provider. Goose can also store keys in your system keychain.

### Codex CLI

Codex requires an OpenAI API key:

```bash
export OPENAI_API_KEY=sk-...
```

## Re-authentication

If your LLM provider credentials expire:

- kardbrd-agent checks authentication **at startup** and **before each card session**
- On auth failure, the agent posts an error comment on the card with specific re-auth instructions
- A :octicons-stop-16: reaction is added to the triggering comment
- The agent continues running and retries auth on the next card event

To re-authenticate without restarting:

| Executor | How to re-authenticate |
|----------|----------------------|
| Claude | Run `claude auth login` or update `ANTHROPIC_API_KEY` |
| Goose | Update the provider-specific API key, or run `goose configure` |
| Codex | Update `OPENAI_API_KEY` |
