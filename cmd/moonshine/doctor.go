package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// checkStatus is the outcome of a single doctor check.
type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
	statusSkip checkStatus = "skip"
)

type doctorCheck struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
}

var (
	doctorLanguage string
	doctorArch     string
)

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	GroupID: "config",
	Short:   "Check build/runtime prerequisites and suggest fixes",
	Long: `Checks the tools and files moonshine-go needs, replacing "run a command
and see what error it throws" with one proactive report. Two tiers:

  - Build-time (only matters before libmoonshine exists): cmake, a C++
    compiler, git-lfs, and a Go toolchain -- what 'make buildlib'/'make
    build' need. Also checks whether cgo is enabled, which 'live' needs for
    microphone capture (transcribe/setup/tts don't).
  - Runtime: whether --lib-dir/MOONSHINE_LIB_DIR resolves and libmoonshine
    actually dlopens, whether an STT model is downloaded for the given
    (--language, --arch) *and* separately for whatever live.arch is
    configured (live's arch is independent of --arch/stt.arch -- see
    'moonshine live --help'), and best-effort checks for GCS credentials
    (gs:// input) and TTS voice assets (--g2p-root).

Exits non-zero if any check fails; warnings and skips don't affect the exit
code -- they flag optional capabilities (gs:// input, tts) that aren't
needed unless you use them.`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().StringVar(&doctorLanguage, "language", "en", "STT (language, arch) pair to check the model directory for (config key: stt.language)")
	doctorCmd.Flags().StringVar(&doctorArch, "arch", "tiny", "STT (language, arch) pair to check the model directory for (config key: stt.arch)")
}

func runDoctor(cmd *cobra.Command, args []string) error {
	checks := []doctorCheck{
		checkCmake(),
		checkCompiler(),
		checkGitLFS(),
		checkGoToolchain(),
		checkCGo(),
		checkLibrary(),
	}
	checks = append(checks, checkModels(cmd)...)
	checks = append(checks, checkGCSCredentials(), checkTTSAssets())

	if jsonOutput() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Checks []doctorCheck `json:"checks"`
		}{checks}); err != nil {
			return err
		}
	} else {
		printDoctorReport(checks)
	}

	for _, c := range checks {
		if c.Status == statusFail {
			return fmt.Errorf("doctor: one or more checks failed (see above)")
		}
	}
	return nil
}

func printDoctorReport(checks []doctorCheck) {
	fmt.Println(header("moonshine doctor"))
	fmt.Println(separator())
	var ok, warn, fail, skip int
	for _, c := range checks {
		fmt.Printf("  %s %-34s %s\n", statusBadge(c.Status), c.Name, c.Detail)
		switch c.Status {
		case statusOK:
			ok++
		case statusWarn:
			warn++
		case statusFail:
			fail++
		case statusSkip:
			skip++
		}
	}
	fmt.Println(separator())
	fmt.Printf("%s %d ok, %d warn, %d fail, %d skip\n", muted("summary:"), ok, warn, fail, skip)
}

func statusBadge(s checkStatus) string {
	switch s {
	case statusOK:
		return stylePass.Render("[ OK ]")
	case statusWarn:
		return styleWarn.Render("[WARN]")
	case statusFail:
		return styleFail.Render("[FAIL]")
	default:
		return styleMuted.Render("[SKIP]")
	}
}

// --- build-time checks -------------------------------------------------

func checkCmake() doctorCheck {
	path, err := exec.LookPath("cmake")
	if err != nil {
		return doctorCheck{"cmake", statusFail,
			"not found in PATH -- needed by 'make buildlib' to compile libmoonshine; " + installHintCmake()}
	}
	out, _ := exec.Command(path, "--version").Output()
	return doctorCheck{"cmake", statusOK, firstLine(string(out))}
}

func checkCompiler() doctorCheck {
	for _, c := range []string{"c++", "clang++", "g++"} {
		path, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		out, _ := exec.Command(path, "--version").Output()
		return doctorCheck{"C++ compiler", statusOK, fmt.Sprintf("%s -- %s", c, firstLine(string(out)))}
	}
	hint := "install a C++20 compiler"
	switch runtime.GOOS {
	case "darwin":
		hint = "install with: xcode-select --install"
	case "linux":
		hint = "install with: sudo apt install build-essential (Debian/Ubuntu) or your distro's equivalent"
	}
	return doctorCheck{"C++ compiler", statusFail,
		"no c++/clang++/g++ found in PATH -- needed by 'make buildlib'; " + hint}
}

func checkGitLFS() doctorCheck {
	path, err := exec.LookPath("git-lfs")
	if err != nil {
		hint := "install with: brew install git-lfs"
		if runtime.GOOS == "linux" {
			hint = "install with: sudo apt install git-lfs (Debian/Ubuntu) or your distro's equivalent, then run 'git lfs install'"
		}
		return doctorCheck{"git-lfs", statusFail,
			"not found in PATH -- the moonshine checkout ships several files as LFS pointers (embedded C++ sources, onnxruntime binaries, TTS voice assets) needed to build/run libmoonshine; " + hint}
	}
	out, _ := exec.Command(path, "version").Output()
	return doctorCheck{"git-lfs", statusOK, firstLine(string(out))}
}

func checkGoToolchain() doctorCheck {
	path, err := exec.LookPath("go")
	if err != nil {
		return doctorCheck{"Go toolchain", statusWarn,
			"'go' not found in PATH -- fine if you only run a prebuilt moonshine binary; needed to 'make build' from source"}
	}
	out, _ := exec.Command(path, "version").Output()
	return doctorCheck{"Go toolchain", statusOK, firstLine(string(out))}
}

func checkCGo() doctorCheck {
	path, err := exec.LookPath("go")
	if err != nil {
		return doctorCheck{"cgo (for 'live')", statusSkip, "'go' not found in PATH, can't check CGO_ENABLED"}
	}
	out, err := exec.Command(path, "env", "CGO_ENABLED").Output()
	if err != nil {
		return doctorCheck{"cgo (for 'live')", statusWarn, "could not determine CGO_ENABLED"}
	}
	val := strings.TrimSpace(string(out))
	if val != "1" {
		return doctorCheck{"cgo (for 'live')", statusWarn,
			fmt.Sprintf("CGO_ENABLED=%s -- building 'live' needs a C toolchain and CGO_ENABLED=1 (mic capture uses gen2brain/malgo); transcribe/setup/tts are unaffected", val)}
	}
	return doctorCheck{"cgo (for 'live')", statusOK, "CGO_ENABLED=1"}
}

func installHintCmake() string {
	switch runtime.GOOS {
	case "darwin":
		return "install with: brew install cmake"
	case "linux":
		return "install with: sudo apt install cmake (Debian/Ubuntu) or your distro's equivalent"
	default:
		return "install cmake 3.22+ for your platform"
	}
}

// --- runtime checks ------------------------------------------------------

func checkLibrary() doctorCheck {
	if err := moonshine.Load(viper.GetString("lib.dir")); err != nil {
		return doctorCheck{"libmoonshine", statusFail,
			err.Error() + " -- run 'make buildlib MOONSHINE_SRC=/path/to/moonshine' (see README's \"Build libmoonshine\")"}
	}
	version, _ := moonshine.Version()
	return doctorCheck{"libmoonshine", statusOK, fmt.Sprintf("loaded %s (version %d)", moonshine.LibPath(), version)}
}

// checkModels checks the model directory for both the transcribe/setup arch
// (--language/--arch on doctor itself, defaulting through stt.language/
// stt.arch) and the live arch (live.arch config key) -- these are
// independent settings (see cmd/moonshine/live.go), so a model present for
// one doesn't mean the other is. Only reports a second row when the two
// archs actually differ, to avoid a redundant duplicate when they match.
func checkModels(cmd *cobra.Command) []doctorCheck {
	language := flagOrConfig(cmd, "language", "stt.language", doctorLanguage)
	transcribeArch := flagOrConfig(cmd, "arch", "stt.arch", doctorArch)
	liveArch := viper.GetString("live.arch")

	checks := []doctorCheck{modelCheck("STT model (transcribe)", language, transcribeArch)}
	if liveArch != transcribeArch {
		checks = append(checks, modelCheck("STT model (live)", language, liveArch))
	}
	return checks
}

func modelCheck(name, language, archFlag string) doctorCheck {
	arch, err := modelArchFromFlag(archFlag)
	if err != nil {
		return doctorCheck{name, statusFail, err.Error()}
	}
	if !moonshine.Loaded() {
		return doctorCheck{name, statusSkip, "libmoonshine isn't loaded, can't resolve which directory to check"}
	}
	dir, err := sttModelDir(language, arch)
	if err != nil {
		return doctorCheck{name, statusWarn, err.Error()}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return doctorCheck{name, statusWarn,
			fmt.Sprintf("no files at %s -- run `moonshine setup --language %s --arch %s`", dir, language, archFlag)}
	}
	return doctorCheck{name, statusOK, fmt.Sprintf("%s/%s: %d file(s) at %s", language, archFlag, len(entries), dir)}
}

func checkGCSCredentials() doctorCheck {
	name := "GCS credentials (for gs:// input)"
	if cred := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); cred != "" {
		if _, err := os.Stat(cred); err == nil {
			return doctorCheck{name, statusOK, "GOOGLE_APPLICATION_CREDENTIALS=" + cred}
		}
		return doctorCheck{name, statusWarn, "GOOGLE_APPLICATION_CREDENTIALS is set but the file doesn't exist: " + cred}
	}
	if home, err := os.UserHomeDir(); err == nil {
		adc := filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
		if _, err := os.Stat(adc); err == nil {
			return doctorCheck{name, statusOK, "found " + adc}
		}
	}
	return doctorCheck{name, statusSkip,
		"no Application Default Credentials found -- only needed for gs:// input; run `gcloud auth application-default login` if you plan to use it"}
}

func checkTTSAssets() doctorCheck {
	name := "TTS voice assets (--g2p-root)"
	root := viper.GetString("tts.g2p_root")
	if root == "" {
		return doctorCheck{name, statusSkip,
			"tts.g2p_root not set -- only needed for `moonshine tts`; set moonshine.src_dir or pass --g2p-root (see 'moonshine tts --help')"}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return doctorCheck{name, statusWarn, fmt.Sprintf("tts.g2p_root=%s does not exist or isn't a directory", root)}
	}
	return doctorCheck{name, statusOK, root}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
