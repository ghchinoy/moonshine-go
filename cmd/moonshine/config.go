package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configCmd = &cobra.Command{
	Use:     "config",
	GroupID: "config",
	Short:   "List or set moonshine-go's configuration file entries",
	Long: `Manage the config file at $XDG_CONFIG_HOME/moonshine/config.yaml (falls
back to ~/.config/moonshine/config.yaml).

Priority (highest to lowest): CLI flag > env var > config.yaml > built-in
default. "moonshine config list" shows the currently effective value and
where it comes from; it does not simulate any other command's flags.`,
}

// configKeys documents every key this CLI reads from config.yaml. Keep in
// sync with root.go's initViper defaults/BindEnv calls and each command's
// flag descriptions.
type configKeyDoc struct {
	Key     string
	EnvVars []string
	Desc    string
}

var configKeys = []configKeyDoc{
	{"lib.dir", []string{"MOONSHINE_LIB_DIR"}, "Directory containing libmoonshine.{dylib,so}"},
	{"model.dir", []string{"MOONSHINE_MODEL_DIR", "MOONSHINE_VOICE_CACHE"}, "Root directory for downloaded STT models / shared TTS cache"},
	{"moonshine.src_dir", []string{"MOONSHINE_SRC"}, "Local moonshine checkout (also used by 'make buildlib'); derives tts.g2p_root's default"},
	{"stt.language", nil, "Default --language for setup/transcribe/live"},
	{"stt.arch", nil, "Default --arch for setup/transcribe"},
	{"live.arch", nil, "Default --arch for live (its own key, independent of stt.arch, since streaming archs need a different default)"},
	{"tts.language", nil, "Default --language for tts"},
	{"tts.voice", nil, "Default --voice for tts"},
	{"tts.speed", nil, "Default --speed for tts"},
	{"tts.g2p_root", nil, "Default --g2p-root for tts; derived from moonshine.src_dir if unset"},
}

func configFilePath() string {
	return filepath.Join(xdgConfigDir(), ConfigFileName+".yaml")
}

var configListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"show", "ls"},
	Short:   "List effective configuration values and where each comes from",
	RunE:    runConfigList,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a key in the config file",
	Long: `Sets a single key in config.yaml, creating the file if it doesn't exist
yet. Only writes the key(s) you've explicitly set via this command (plus
anything already in the file) -- it does not dump every current default
into the file.`,
	Example: `  moonshine config set moonshine.src_dir ~/projects/github/moonshine
  moonshine config set stt.arch base
  moonshine config set live.arch base-streaming
  moonshine config set tts.voice piper_en_US-amy-low`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configFilePath()
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Printf("%s %s\n", styleWarn.Render("(not found)"), path)
			return nil
		}
		fmt.Println(path)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configListCmd, configSetCmd, configPathCmd)
}

type configEntry struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	Provenance string `json:"provenance"`
}

func runConfigList(cmd *cobra.Command, args []string) error {
	fileValues := readConfigFileOnly()

	entries := make([]configEntry, 0, len(configKeys))
	for _, ck := range configKeys {
		val := viper.GetString(ck.Key)
		prov := "default"
		for _, ev := range ck.EnvVars {
			if os.Getenv(ev) != "" {
				prov = "env:" + ev
				break
			}
		}
		if prov == "default" {
			if _, ok := fileValues[ck.Key]; ok {
				prov = "file"
			}
		}
		entries = append(entries, configEntry{Key: ck.Key, Value: val, Provenance: prov})
	}

	if jsonOutput() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			ConfigFile string        `json:"config_file"`
			Settings   []configEntry `json:"settings"`
		}{configFilePath(), entries})
	}

	fmt.Println(header("moonshine-go configuration"))
	fmt.Println(separator())
	for _, e := range entries {
		val := e.Value
		if val == "" {
			val = "(unset)"
		}
		fmt.Printf("  %-20s %-42s %s\n", e.Key, val, provenanceLabel(e.Provenance))
	}
	fmt.Println(separator())
	path := configFilePath()
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("%s %s\n", muted("config file:"), path)
	} else {
		fmt.Printf("%s %s\n", muted("config file:"), styleWarn.Render("not found -- 'moonshine config set <key> <value>' will create it"))
	}
	return nil
}

func provenanceLabel(p string) string {
	switch {
	case p == "default":
		return styleMuted.Render("default")
	case p == "file":
		return styleAccent.Render("file")
	case strings.HasPrefix(p, "env:"):
		return stylePass.Render(p)
	default:
		return styleMuted.Render(p)
	}
}

// readConfigFileOnly re-reads config.yaml on its own (isolated from the
// global viper instance's defaults/env/flag layers) so callers can tell
// "is this key literally present in the file" from "is this just the
// resolved default".
func readConfigFileOnly() map[string]any {
	out := map[string]any{}
	path := viper.ConfigFileUsed()
	if path == "" {
		path = configFilePath()
	}
	if _, err := os.Stat(path); err != nil {
		return out
	}
	fv := viper.New()
	fv.SetConfigFile(path)
	if err := fv.ReadInConfig(); err != nil {
		return out
	}
	flattenInto(out, "", fv.AllSettings())
	return out
}

func flattenInto(out map[string]any, prefix string, m map[string]any) {
	for k, v := range m {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			flattenInto(out, full, sub)
			continue
		}
		out[full] = v
	}
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]

	known := false
	knownKeys := make([]string, len(configKeys))
	for i, ck := range configKeys {
		knownKeys[i] = ck.Key
		if ck.Key == key {
			known = true
		}
	}
	if !known {
		sort.Strings(knownKeys)
		return fmt.Errorf("unknown config key %q\n\n%s %s", key, muted("known keys:"), strings.Join(knownKeys, ", "))
	}

	path := configFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// A separate, isolated Viper instance for reading/writing the file
	// itself -- deliberately not the global `viper` singleton, which has
	// defaults/env/flags layered in. Writing that back out would dump every
	// current default into the file instead of just the key being set.
	fv := viper.New()
	fv.SetConfigFile(path)
	fv.SetConfigType("yaml")
	if _, err := os.Stat(path); err == nil {
		if err := fv.ReadInConfig(); err != nil {
			return fmt.Errorf("reading existing config: %w", err)
		}
	}
	fv.Set(key, value)
	if err := fv.WriteConfigAs(path); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("%s %s = %s\n", stylePass.Render("Set"), key, value)
	fmt.Printf("%s %s\n", muted("config file:"), path)
	return nil
}
