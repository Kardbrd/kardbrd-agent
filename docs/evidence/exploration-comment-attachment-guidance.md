# Comment and attachment guidance: Go CLI evidence

Card: [kVykpA0e](https://app.kardbrd.com/c/kVykpA0e/)

## Executive summary

The Go CLI previously advertised comment and attachment commands with only a
one-line description. This change adds the card's content-based choice to all
three relevant command help pages and aligns the shared response prompt. It
does not change command arguments, publication, upload, or report processing.
The 250-word target remains advice, and the help explicitly says it is not a
publication limit. A Markdown report can keep its full UTF-8 text, real
newlines, code fences, and fenced Mermaid source.

## Current state and relevant paths

- `internal/cli/client_commands.go:586-633` defines `comment add`. Its run
  function still calls `client.AddComment` with `expandPublishedText`.
- `internal/cli/client_commands.go:754-795` defines `attachment upload` and
  `attachment markdown`. Both still call their existing API client methods.
  `--content-file PATH` reads bytes from a file; `--content-file -` reads stdin.
- `internal/cli/client_commands.go:1074-1095` implements content flag input.
  The file and stdin paths are read without newline replacement.
- `internal/prompt/prompt.go:67-84` supplies the shared completion instructions.
  Current main directs the agent to return its final response normally; the
  manager publishes it as one terminal comment with the requester mention.
  The added wording keeps that publication path and explains when to point
  from the short response to a full report attachment.
- `internal/api/attachments.go:17-79` handles the existing presign, upload,
  and confirmation path. It was inspected but not modified.

## Architecture constraints and approach

The card owns Go CLI help and limited shared prompt wording. The Web owner
owns MCP tool descriptions and the real upload-to-preview check. The card
states that a detailed report is judged by its content, not an automatic word
count cutoff. For consistency, one `commentAttachmentGuidance` help string is
used on the three CLI commands. The Markdown help shows the exact supported
command form:

```sh
kardbrd attachment markdown CARD_ID --filename report.md --content-file PATH
```

The prompt change preserves main's manager-owned final publication and
requester mention, while telling the agent to keep a complete report in the
attachment when a sensible comment cannot contain the needed detail. The
alternative of adding automatic word counting, conversion, or report
truncation would change
publication behavior and is outside this card.

The first commit was based on an older local `main`; GitHub showed a conflict
with `origin/main` at `95cf115`. The branch was rebased onto that current main
and the prompt wording was reconciled with its manager-owned final response.

## Generated help inspected

The following commands were run against this branch with
`/usr/local/go/bin/go run ./cmd/kardbrd`:

```text
comment add --help: Use comments for the result, important reasoning,
decisions, blockers, and questions the human should read directly.
attachment markdown --help: Upload markdown content as an attachment;
preserve real newlines, code fences, and fenced Mermaid source; use
--content-file PATH or --content-file - for stdin.
attachment upload --help: Upload a file to a card; for a Markdown report,
use attachment markdown CARD_ID --filename report.md --content-file PATH.
```

Each generated help page displayed the same full choice guidance, including
the advisory 250-word target, a pointer to the attachment, and the need to
keep the complete report. No behavior was changed, so no behavioral RED was
claimed.

## Verification

| Check | Result |
| --- | --- |
| `go test ./...` | PASS, all packages |
| `go vet ./...` | PASS |
| `pre-commit run --all-files` | PASS, all applicable hooks |
| `git diff --check` | PASS |

The first full run after rebase found two existing prompt tests that require
the original 250-word sentence verbatim. The sentence was retained, the
advisory clarification was placed after it, and the focused and full suites
then passed.

The rebased branch's Go CLI uploaded a synthetic 239-byte Markdown file using
`--content-file`. The returned attachment is `Ee8QWogn`, filename
`kVykpA0e-preview-check-rebased.md`, content type
`text/markdown; charset=utf-8`.
It includes `café — 東京`, separate paragraphs, a fenced Go block, and fenced
Mermaid source. The downloaded bytes matched the source file exactly:
SHA-256 `dbbd70dea38464f9f574288ec62f9a718fb1ae8051081682ec37a82fbc439b43`.
This proves the Go upload and download path for this fixture. It does not
prove how Web renders the preview.
An earlier pre-rebase fixture, `LeJQXqz1`, remains attached; use the rebased
fixture `Ee8QWogn` for the Web check.

## Open coordination item

The linked Web card ID `JVj86O08` returned HTTP 404 to this Go CLI token.
The Web owner or supervisor needs to open attachment `Ee8QWogn` on this card
and record the actual preview result and supported preview-size budget on the
Web counterpart. Merge, release, and runtime rollout are outside this change.
