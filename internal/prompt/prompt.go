package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Request struct {
	CardID         string
	CardMarkdown   string
	Command        string
	CommentContent string
	AuthorName     string
	BoardID        string
	CWD            string
}

func LoadAgentFiles(cwd string) (soul string, rules string) {
	if cwd == "" {
		return "", ""
	}
	soul = readOptional(filepath.Join(cwd, "SOUL.md"))
	rules = readOptional(filepath.Join(cwd, "RULES.md"))
	return soul, rules
}

func LoadKnowledge(cwd string) string {
	if cwd == "" {
		return ""
	}
	knowledgeDir := filepath.Join(cwd, "knowledge")
	info, err := os.Stat(knowledgeDir)
	if err != nil || !info.IsDir() {
		return ""
	}

	docs := knowledgeFromIndex(knowledgeDir)
	if len(docs) == 0 {
		docs = allMarkdownKnowledge(knowledgeDir)
	}
	if len(docs) == 0 {
		return ""
	}
	return "## Knowledge\n\n" + strings.Join(docs, "\n\n") + "\n\n"
}

func Build(req Request) string {
	soul, rules := LoadAgentFiles(req.CWD)
	knowledge := LoadKnowledge(req.CWD)

	var preamble strings.Builder
	if soul != "" {
		fmt.Fprintf(&preamble, "## Agent Identity\n\n%s\n\n", soul)
	}
	if rules != "" {
		fmt.Fprintf(&preamble, "## Agent Rules\n\n%s\n\n", rules)
	}
	if knowledge != "" {
		preamble.WriteString(knowledge)
	}

	responseInstructions := `
## Progress Updates and Final Response

You may post progress updates to the card while you work. Comments, attachments,
links, reactions, and other card updates are allowed during the run; they do not
finish the task and are never a substitute for the final response.

Return your final response normally when the task is complete. Do not use ` + "`kardbrd comment add`" + ` to publish the completion response. The agent manager will publish it once as the terminal summary and mention the requester.
`

	labelInstructions := ""
	cliInstructions := ""
	if req.BoardID != "" {
		labelInstructions = fmt.Sprintf(`
## Labels

Cards may have labels (shown as "Labels: ..." in card markdown).
Available CLI commands:
- `+"`"+`kardbrd board labels %s`+"`"+` to discover available labels
- `+"`"+`kardbrd card update %s --label-ids ID1 --label-ids ID2`+"`"+` to replace labels
- `+"`"+`kardbrd card update %s --clear-labels`+"`"+` to remove every label

**Important:** `+"`"+`--label`+"`"+` and `+"`"+`--label-ids`+"`"+` are repeatable aliases for the complete desired label set. Include labels you want to keep; `+"`"+`--clear-labels`+"`"+` cannot be combined with either alias.
`, req.BoardID, req.CardID, req.CardID)

		cliInstructions = fmt.Sprintf(`
## kardbrd CLI Reference

The `+"`"+`kardbrd`+"`"+` CLI is available for board operations. Key commands:
- `+"`"+`kardbrd md card %s`+"`"+` - get this card as markdown
- `+"`"+`kardbrd md board %s`+"`"+` - get board as markdown
- `+"`"+`kardbrd comment add %s "message"`+"`"+` - add comment to this card
- `+"`"+`kardbrd card update %s --title "..." --description "..."`+"`"+` - update card
- `+"`"+`kardbrd card create --board %s --list LIST_ID --title "..."`+"`"+` - create card
- `+"`"+`kardbrd card move %s --list LIST_ID`+"`"+` - move card

Environment variables `+"`"+`KARDBRD_TOKEN`+"`"+` and `+"`"+`KARDBRD_API_URL`+"`"+` are pre-configured.
`, req.CardID, req.BoardID, req.CardID, req.CardID, req.BoardID, req.CardID)
	}

	if strings.HasPrefix(req.Command, "/") {
		return fmt.Sprintf(`%s%s

---

## Context

**Card ID:** %s
**Triggered by:** @%s
**Comment:** %s

## Card Content

%s
%s%s%s
`, preamble.String(), req.Command, req.CardID, req.AuthorName, req.CommentContent, req.CardMarkdown, labelInstructions, cliInstructions, responseInstructions)
	}

	return fmt.Sprintf(`%s## Task Request

%s

---

## Card Context

**Card ID:** %s

%s
%s%s
---

**Requested by:** @%s

Please complete this request.
%s
`, preamble.String(), req.CommentContent, req.CardID, req.CardMarkdown, labelInstructions, cliInstructions, req.AuthorName, responseInstructions)
}

func ExtractCommand(commentContent string, mentionKeyword string) string {
	contentLower := strings.ToLower(commentContent)
	mentionLower := strings.ToLower(mentionKeyword)
	index := strings.Index(contentLower, mentionLower)
	if index == -1 {
		return strings.TrimSpace(commentContent)
	}
	afterMention := strings.TrimSpace(commentContent[index+len(mentionKeyword):])
	if afterMention == "" {
		return strings.TrimSpace(commentContent)
	}
	return afterMention
}

func readOptional(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

type knowledgeIndex struct {
	Documents []knowledgeDocument `yaml:"documents"`
}

type knowledgeDocument struct {
	Path       string `yaml:"path"`
	Title      string `yaml:"title"`
	Priority   string `yaml:"priority"`
	AlwaysLoad bool   `yaml:"always_load"`
}

func knowledgeFromIndex(knowledgeDir string) []string {
	data, err := os.ReadFile(filepath.Join(knowledgeDir, "index.yaml"))
	if err != nil {
		return nil
	}
	var index knowledgeIndex
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil
	}
	var docs []string
	for _, doc := range index.Documents {
		if !doc.AlwaysLoad && doc.Priority != "high" {
			continue
		}
		path := filepath.Join(knowledgeDir, doc.Path)
		content := readOptional(path)
		if content == "" {
			continue
		}
		title := doc.Title
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		docs = append(docs, fmt.Sprintf("### %s\n\n%s", title, content))
	}
	return docs
}

func allMarkdownKnowledge(knowledgeDir string) []string {
	matches, err := filepath.Glob(filepath.Join(knowledgeDir, "*.md"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	var docs []string
	for _, path := range matches {
		content := readOptional(path)
		if content == "" {
			continue
		}
		title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		docs = append(docs, fmt.Sprintf("### %s\n\n%s", title, content))
	}
	return docs
}
