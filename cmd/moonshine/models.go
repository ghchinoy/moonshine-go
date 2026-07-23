package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type modelCatalogEntry struct {
	Name        string `json:"name"`
	ArchID      uint32 `json:"arch_id"`
	Streaming   bool   `json:"streaming"`
	Description string `json:"description"`
}

var knownModels = []modelCatalogEntry{
	{
		Name:        "tiny",
		ArchID:      moonshine.ModelArchTiny,
		Streaming:   false,
		Description: "Fast, lightweight non-streaming model for offline transcription",
	},
	{
		Name:        "base",
		ArchID:      moonshine.ModelArchBase,
		Streaming:   false,
		Description: "Larger non-streaming model with higher accuracy",
	},
	{
		Name:        "tiny-streaming",
		ArchID:      moonshine.ModelArchTinyStreaming,
		Streaming:   true,
		Description: "Low-latency streaming model for live speech",
	},
	{
		Name:        "base-streaming",
		ArchID:      moonshine.ModelArchBaseStreaming,
		Streaming:   true,
		Description: "Base-size streaming model (defined in C API; not currently published in English CDN catalog)",
	},
	{
		Name:        "small-streaming",
		ArchID:      moonshine.ModelArchSmallStreaming,
		Streaming:   true,
		Description: "Small streaming model for complex audio",
	},
	{
		Name:        "medium-streaming",
		ArchID:      moonshine.ModelArchMediumStreaming,
		Streaming:   true,
		Description: "Largest streaming model for maximum accuracy",
	},
}

type modelStatus struct {
	Name        string `json:"name"`
	ArchID      uint32 `json:"arch_id"`
	Streaming   bool   `json:"streaming"`
	Description string `json:"description"`
	Downloaded  bool   `json:"downloaded"`
	Path        string `json:"path,omitempty"`
	FileCount   int    `json:"file_count,omitempty"`
}

var modelsLanguage string

var modelsCmd = &cobra.Command{
	Use:     "models",
	GroupID: "config",
	Short:   "List available speech-to-text models and local download status",
	Long: `Lists all supported speech-to-text model architectures, categorizing
them by type (streaming vs non-streaming) and checking whether each model is
currently downloaded in the local model directory (--model-dir).`,
	Args: cobra.NoArgs,
	RunE: runModels,
}

func init() {
	modelsCmd.Flags().StringVar(&modelsLanguage, "language", "en", "STT language pair to check local model files for (config key: stt.language)")
}

func runModels(cmd *cobra.Command, args []string) error {
	language := flagOrConfig(cmd, "language", "stt.language", modelsLanguage)
	modelRootDir := viper.GetString("model.dir")

	_ = moonshine.Load(viper.GetString("lib.dir"))

	var results []modelStatus

	for _, entry := range knownModels {
		status := modelStatus{
			Name:        entry.Name,
			ArchID:      entry.ArchID,
			Streaming:   entry.Streaming,
			Description: entry.Description,
		}

		checkModelDownload(language, modelRootDir, &status)
		results = append(results, status)
	}

	if jsonOutput() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Language string        `json:"language"`
			ModelDir string        `json:"model_dir"`
			Models   []modelStatus `json:"models"`
		}{
			Language: language,
			ModelDir: modelRootDir,
			Models:   results,
		})
	}

	printModelsTable(language, modelRootDir, results)
	return nil
}

func checkModelDownload(language string, modelRootDir string, status *modelStatus) {
	if status.ArchID != moonshine.ModelArchBaseStreaming && moonshine.Loaded() {
		manifest, err := moonshine.GetSTTDependencies(language,
			moonshine.Option{Name: "model_arch", Value: fmt.Sprintf("%d", status.ArchID)})
		if err == nil {
			dir, err := moonshine.PrimaryModelDir(modelRootDir, manifest)
			if err == nil {
				if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
					status.Downloaded = true
					status.Path = dir
					status.FileCount = len(entries)
					return
				}
			}
		}
	}

	if modelRootDir == "" {
		return
	}

	_ = filepath.Walk(modelRootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		dirName := strings.ToLower(info.Name())
		target := strings.ToLower(status.Name)
		if strings.Contains(dirName, target) {
			entries, err := os.ReadDir(path)
			if err == nil && len(entries) > 0 {
				hasORT := false
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".ort") || strings.HasSuffix(e.Name(), ".bin") {
						hasORT = true
						break
					}
				}
				if hasORT {
					status.Downloaded = true
					status.Path = path
					status.FileCount = len(entries)
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
}

func printModelsTable(language, modelRootDir string, models []modelStatus) {
	fmt.Println(header(fmt.Sprintf("moonshine models (language: %s)", language)))
	fmt.Println(muted(fmt.Sprintf("model.dir: %s", modelRootDir)))
	fmt.Println(separator())

	for _, m := range models {
		typeStr := "non-streaming"
		if m.Streaming {
			typeStr = "streaming    "
		}

		badge := styleMuted.Render("[not cached]")
		if m.Downloaded {
			badge = stylePass.Render("[downloaded]")
		}

		fmt.Printf("  %-16s %s %s", styleAccent.Render(m.Name), typeStr, badge)
		if m.Downloaded {
			fmt.Printf(" %s (%d files)", muted(m.Path), m.FileCount)
		}
		fmt.Println()
		fmt.Printf("    %s\n", muted(m.Description))
	}
	fmt.Println(separator())
}
