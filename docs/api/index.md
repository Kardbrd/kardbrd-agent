# API Reference

Auto-generated from source code docstrings.

## Executor Protocol

::: kardbrd_agent.executor.Executor
    options:
      show_source: false
      members:
        - execute
        - build_prompt
        - extract_command
        - check_auth

## ExecutorResult

::: kardbrd_agent.executor.ExecutorResult

## AuthStatus

::: kardbrd_agent.executor.AuthStatus

## ClaudeExecutor

::: kardbrd_agent.executor.ClaudeExecutor
    options:
      members:
        - execute
        - check_auth

## GooseExecutor

::: kardbrd_agent.goose_executor.GooseExecutor
    options:
      members:
        - execute
        - check_auth

## RuleEngine

::: kardbrd_agent.rules.RuleEngine

## Rule

::: kardbrd_agent.rules.Rule

## Schedule

::: kardbrd_agent.rules.Schedule

## BoardConfig

::: kardbrd_agent.rules.BoardConfig

## ProxyManager

::: kardbrd_agent.manager.ProxyManager
    options:
      show_source: false
      members:
        - start
        - stop

## WorktreeManager

::: kardbrd_agent.worktree.WorktreeManager
    options:
      members:
        - create_worktree
        - remove_worktree
        - get_worktree_path
        - list_worktrees

## ScheduleManager

::: kardbrd_agent.scheduler.ScheduleManager
    options:
      show_source: false
      members:
        - start
        - stop
