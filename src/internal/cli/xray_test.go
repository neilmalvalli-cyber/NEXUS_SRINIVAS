package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/xray"
)

func runXRay(t *testing.T, argv ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := Run(append([]string{"xray"}, argv...), Env{Stdin: strings.NewReader(""), Stdout: &o, Stderr: &e})
	return code, o.String(), e.String()
}

func TestXRayCommand(t *testing.T) {
	cases, err := xray.BuildLab(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]xray.LabCase{}
	for _, c := range cases {
		byName[c.Name] = c
	}

	code, out, _ := runXRay(t, byName["clean"].Path)
	if code != ExitOK || !strings.Contains(out, "health: HEALTHY") || !strings.Contains(out, "file not modified") {
		t.Fatalf("clean: exit %d\n%s", code, out)
	}

	torn := byName["torn-tail"].Path
	before, _ := os.ReadFile(torn)
	code, out, _ = runXRay(t, torn, "--verbose")
	if code != ExitNeedsRepair || !strings.Contains(out, "FRAME_PAYLOAD_TRUNCATED") || !strings.Contains(out, "technical:") {
		t.Fatalf("torn: exit %d\n%s", code, out)
	}
	if after, _ := os.ReadFile(torn); !bytes.Equal(before, after) {
		t.Fatal("read-only xray modified the file")
	}

	code, out, _ = runXRay(t, byName["committed-frame-corrupted"].Path, "--json")
	var rep xray.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if code != ExitError || rep.Counts[xray.Critical] == 0 || rep.Findings[0].Rule != "C6" {
		t.Fatalf("corrupt: exit %d %+v", code, rep.Counts)
	}

	if code, _, _ = runXRay(t, torn, "--repair"); code != ExitUsage {
		t.Fatalf("--repair without --yes: exit %d", code)
	}
	if code, _, _ = runXRay(t, torn, "--yes"); code != ExitUsage {
		t.Fatalf("--yes without --repair: exit %d", code)
	}
	if code := Run([]string{"get", torn, "k", "--json"}, Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}); code != ExitUsage {
		t.Fatalf("--json on get: exit %d", code)
	}

	bak := filepath.Join(t.TempDir(), "bak")
	code, out, _ = runXRay(t, torn, "--repair", "--yes", "--backup-dir", bak)
	if code != ExitOK || !strings.Contains(out, "verified=true") || !strings.Contains(out, "backup: ") {
		t.Fatalf("repair: exit %d\n%s", code, out)
	}
	if code, _, _ = runXRay(t, torn); code != ExitOK {
		t.Fatalf("after repair: exit %d", code)
	}
}
