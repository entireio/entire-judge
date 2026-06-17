package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
)

func TestBrainDataDirsPrefersBrainPlugin(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{filepath.FromSlash("/x/data/judge"), []string{filepath.FromSlash("/x/data/brain"), filepath.FromSlash("/x/data/judge")}},
		{filepath.FromSlash("/x/data/brain"), []string{filepath.FromSlash("/x/data/brain")}},
		{"", []string{""}},
	}
	for _, c := range cases {
		if got := brainDataDirs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("brainDataDirs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func writeBrainManifest(t *testing.T, dataDir, key string) {
	t.Helper()
	dir := brainstore.BrainDir(dataDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"repo_key":"`+key+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveBrainStorageFindsBrainPluginDir is Thomas's reported case: the brain
// was built by `entire brain refresh` (under .../data/brain) but judge was run
// with its own data dir (.../data/judge). Judge must resolve the sibling dir.
func TestResolveBrainStorageFindsBrainPluginDir(t *testing.T) {
	root := t.TempDir()
	judgeData := filepath.Join(root, "judge")
	brainData := filepath.Join(root, "brain")
	writeBrainManifest(t, brainData, "gh/team/project")

	st := resolveBrainStorage(context.Background(),
		fakeRunner{origin: "https://github.com/team/project.git"},
		EntireEnv{PluginDataDir: judgeData}, "/tmp/repo")

	want := brainstore.BrainDir(brainData, "gh/team/project")
	if st.BrainDir != want {
		t.Errorf("resolved brain dir = %q, want the brain plugin dir %q", st.BrainDir, want)
	}
}

// TestResolveBrainStorageFallsBackToJudgeDir covers a brain built by
// `entire judge add`, which writes under judge's own data dir.
func TestResolveBrainStorageFallsBackToJudgeDir(t *testing.T) {
	root := t.TempDir()
	judgeData := filepath.Join(root, "judge")
	writeBrainManifest(t, judgeData, "gh/team/project")

	st := resolveBrainStorage(context.Background(),
		fakeRunner{origin: "https://github.com/team/project.git"},
		EntireEnv{PluginDataDir: judgeData}, "/tmp/repo")

	want := brainstore.BrainDir(judgeData, "gh/team/project")
	if st.BrainDir != want {
		t.Errorf("resolved brain dir = %q, want judge fallback %q", st.BrainDir, want)
	}
}

// TestResolveBrainStorageMissingPointsAtBrainPluginDir checks that when no brain
// exists, the (error-message) path points at the brain plugin dir, not judge's.
func TestResolveBrainStorageMissingPointsAtBrainPluginDir(t *testing.T) {
	root := t.TempDir()
	judgeData := filepath.Join(root, "judge")

	st := resolveBrainStorage(context.Background(),
		fakeRunner{origin: "https://github.com/team/project.git"},
		EntireEnv{PluginDataDir: judgeData}, "/tmp/repo")

	want := brainstore.BrainDir(filepath.Join(root, "brain"), "gh/team/project")
	if st.BrainDir != want {
		t.Errorf("missing-brain dir = %q, want brain plugin dir %q", st.BrainDir, want)
	}
}
