# Explore

Deep-dive exploration of the card's topic in the context of the kardbrd-agent codebase.

## Instructions

1. Read the card description and all comments to understand the topic
2. Explore the codebase systematically through these passes:
   - **Manager pass**: `kardbrd_agent/manager.py` — event handling, session tracking, concurrency
   - **Executor pass**: `kardbrd_agent/executor.py` — Claude CLI spawning, prompt building, output parsing
   - **Worktree pass**: `kardbrd_agent/worktree.py` — git worktree lifecycle, symlinks, setup
   - **Rules pass**: `kardbrd_agent/rules.py` — rule engine, YAML parsing, hot reload
   - **CLI pass**: `kardbrd_agent/cli.py` — Typer commands, configuration, startup
   - **Tests pass**: `kardbrd_agent/tests/` — test patterns, fixtures, coverage
3. Look at related cards mentioned in the description (use `!cardId` references)
4. Check the board for related cards and context
5. Post findings as a structured comment on the card with:
   - Current state (what exists)
   - Relevant code paths and files
   - Proposed approach or options
   - Open questions for the operator

## Guidelines

- Be thorough but focused on what's relevant to the card
- Reference specific files and line numbers
- Note any architectural constraints or patterns to follow
- If the card mentions other cards, fetch and read them for context
