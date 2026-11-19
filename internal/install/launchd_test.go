package install

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/config"
)

type plistNode struct {
	name   string
	text   string
	values []plistNode
}

func (n *plistNode) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	n.name = start.Name.Local
	var text strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			var child plistNode
			if err := child.UnmarshalXML(d, tok); err != nil {
				return err
			}
			n.values = append(n.values, child)
		case xml.CharData:
			text.Write(tok)
		case xml.EndElement:
			n.text = text.String()
			return nil
		}
	}
}

type plistDoc struct {
	XMLName xml.Name  `xml:"plist"`
	Dict    plistNode `xml:"dict"`
}

func plistFields(t *testing.T, content string) map[string]plistNode {
	t.Helper()
	var doc plistDoc
	if err := xml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("plist is not valid XML: %v\n%s", err, content)
	}
	fields := map[string]plistNode{}
	key := ""
	for _, node := range doc.Dict.values {
		if node.name == "key" {
			key = strings.TrimSpace(node.text)
			continue
		}
		if key == "" {
			t.Fatalf("plist value %q has no key", node.text)
		}
		fields[key] = node
		key = ""
	}
	return fields
}

func wantPlistString(t *testing.T, fields map[string]plistNode, key, want string) {
	t.Helper()
	node, ok := fields[key]
	if !ok {
		t.Fatalf("plist missing %q", key)
	}
	if node.name != "string" || node.text != want {
		t.Fatalf("plist %s = <%s>%q, want <%s>%q", key, node.name, node.text, "string", want)
	}
}

func wantPlistTrue(t *testing.T, fields map[string]plistNode, key string) {
	t.Helper()
	node, ok := fields[key]
	if !ok {
		t.Fatalf("plist missing %q", key)
	}
	if node.name != "true" {
		t.Fatalf("plist %s = <%s>, want <true>", key, node.name)
	}
}

func checkPlist(t *testing.T, content, executable, configPath, stateDir, logPath string) {
	t.Helper()
	fields := plistFields(t, content)
	wantPlistString(t, fields, "Label", "dev.host-telemetry")
	wantPlistString(t, fields, "ProcessType", "Background")
	wantPlistString(t, fields, "WorkingDirectory", stateDir)
	wantPlistString(t, fields, "StandardOutPath", logPath)
	wantPlistString(t, fields, "StandardErrorPath", logPath)
	wantPlistTrue(t, fields, "RunAtLoad")
	wantPlistTrue(t, fields, "KeepAlive")
	wantPlistTrue(t, fields, "LowPriorityIO")
	args, ok := fields["ProgramArguments"]
	if !ok || args.name != "array" {
		t.Fatalf("plist ProgramArguments = %+v", args)
	}
	var got []string
	for _, arg := range args.values {
		if arg.name != "string" {
			t.Fatalf("ProgramArguments element = <%s>, want <string>", arg.name)
		}
		got = append(got, arg.text)
	}
	want := []string{executable, "run", "--config", configPath}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ProgramArguments = %q, want %q", got, want)
	}
}

func TestLaunchdUserPlan(t *testing.T) {
	home := "/Users/tester"
	target := Target{Platform: "darwin", User: true, UID: 501, Home: home, Executable: "/usr/local/bin/host-telemetry"}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 5 {
		t.Fatalf("actions = %d, want 5", len(actions))
	}

	base := filepath.Join(home, "Library", "Application Support", "host-telemetry")
	configAction, plistAction := actions[0], actions[1]
	if want := filepath.Join(base, "config.yaml"); configAction.Kind != "write" || configAction.Path != want || configAction.Mode != "0600" {
		t.Fatalf("config action = %+v, want %s (0600)", configAction, want)
	}
	wantPlist := filepath.Join(home, "Library", "LaunchAgents", "dev.host-telemetry.plist")
	if plistAction.Kind != "write" || plistAction.Path != wantPlist || plistAction.Mode != "0644" {
		t.Fatalf("plist action = %+v, want %s (0644)", plistAction, wantPlist)
	}
	checkPlist(t, plistAction.Content, "/usr/local/bin/host-telemetry", filepath.Join(base, "config.yaml"), base, filepath.Join(base, "host-telemetry.log"))

	wantCommands := [][]string{
		{"launchctl", "bootstrap", "gui/501", wantPlist},
		{"launchctl", "enable", "gui/501/dev.host-telemetry"},
		{"launchctl", "kickstart", "-k", "gui/501/dev.host-telemetry"},
	}
	for i, want := range wantCommands {
		got := actions[2+i]
		if got.Kind != "exec" || strings.Join(got.Command, " ") != strings.Join(want, " ") {
			t.Fatalf("action %d = %+v, want %q", 2+i, got, want)
		}
	}
}

func TestLaunchdSystemPlan(t *testing.T) {
	target := Target{Platform: "darwin", Executable: "/usr/local/bin/host-telemetry"}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 5 {
		t.Fatalf("actions = %d, want 5", len(actions))
	}

	base := "/Library/Application Support/host-telemetry"
	configAction, plistAction := actions[0], actions[1]
	if want := filepath.Join(base, "config.yaml"); configAction.Kind != "write" || configAction.Path != want || configAction.Mode != "0600" {
		t.Fatalf("config action = %+v, want %s (0600)", configAction, want)
	}
	wantPlist := "/Library/LaunchDaemons/dev.host-telemetry.plist"
	if plistAction.Kind != "write" || plistAction.Path != wantPlist || plistAction.Mode != "0644" {
		t.Fatalf("plist action = %+v, want %s (0644)", plistAction, wantPlist)
	}
	checkPlist(t, plistAction.Content, "/usr/local/bin/host-telemetry", filepath.Join(base, "config.yaml"), base, filepath.Join(base, "host-telemetry.log"))

	wantCommands := [][]string{
		{"launchctl", "bootstrap", "system", wantPlist},
		{"launchctl", "enable", "system/dev.host-telemetry"},
		{"launchctl", "kickstart", "-k", "system/dev.host-telemetry"},
	}
	for i, want := range wantCommands {
		got := actions[2+i]
		if got.Kind != "exec" || strings.Join(got.Command, " ") != strings.Join(want, " ") {
			t.Fatalf("action %d = %+v, want %q", 2+i, got, want)
		}
	}
}

func TestLaunchdDryRunGolden(t *testing.T) {
	target := Target{Platform: "darwin", Home: "/Users/tester", Executable: "/usr/local/bin/host-telemetry"}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	Render(&buf, actions)

	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "darwin", "install-dry-run.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("dry-run output differs from golden:\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
	cfg, err := config.Marshal(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(want, cfg) {
		t.Fatalf("golden is missing the config contents:\n%s", want)
	}
}
