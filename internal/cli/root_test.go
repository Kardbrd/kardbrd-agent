package cli

import (
	"bytes"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestRootCommandNames(t *testing.T) {
	cmd := NewRootCommand()
	got := commandNames(cmd.Commands())
	want := []string{
		"activity",
		"agent",
		"attachment",
		"board",
		"card",
		"checklist",
		"comment",
		"link",
		"list",
		"md",
		"search",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command names mismatch:\nwant: %#v\n got: %#v", want, got)
	}
}

func TestRootHelpDoesNotPrintTokenEnvValue(t *testing.T) {
	t.Setenv("KARDBRD_TOKEN", "secret-token")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"--help"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "secret-token") {
		t.Fatalf("help output leaked token: %s", stdout.String())
	}
}

func commandNames(commands any) []string {
	value := reflect.ValueOf(commands)
	names := make([]string, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		name := value.Index(i).MethodByName("Name").Call(nil)[0].String()
		names = append(names, name)
	}
	return sortedNames(names)
}

func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
