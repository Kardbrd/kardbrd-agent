# Bot comment writing examples

The 250-word limit applies to the exact submitted body, including Markdown, links, and the final requester mention. Keep earlier card comments intact and in order when preparing context.

## Completion

Before: “Done. Everything is fixed. Tests passed.”

After: “Decision: the CLI now sends one comment POST and checks the expanded body before sending. Evidence: the boundary fixtures and CLI retry test pass; a simulated 503 produced one POST. The manager still owns the final response. The Web authenticated enforcement build remains pending. See the attached implementation report for commands and output. @Paul”

## Blocker

Before: “Could not finish; see logs.”

After: “Blocked on the Web counterpart: this worktree contains only the Go agent, and the linked Web card is inaccessible to this token. The agent branch and fixture file are ready for handoff. The Web owner still needs to verify HTTP 400 and MCP error envelopes before a combined release. No deployment was performed. @Paul”

## Review finding

Before: “Tests failed, but it should be okay.”

After: “Review finding: a lost response after comment POST caused a second unkeyed write. The new test proves one attempt and no success reaction on an uncertain response. The precise server result remains unknown, so inspect the card before any operator retry. See the test log attachment for the failure and passing result. @Paul”
