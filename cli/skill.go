// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

type HarnessPaths struct {
	Global string
	Local  string
}

var harnesses = map[string]HarnessPaths{
	"generic":     {"~/.agents/skills", ".agents/skills"},
	"codex":       {"~/.agents/skills", ".agents/skills"},
	"gemini":      {"~/.gemini/skills", ".gemini/skills"},
	"claude":      {"~/.claude/skills", ".claude/skills"},
	"opencode":    {"~/.config/opencode/skills", ".opencode/skills"},
	"pi":          {"~/.pi/agent/skills", ".pi/skills"},
	"copilot":     {"~/.copilot/skills", ".github/skills"},
}

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage AgentSkills for OpenRelik",
		Long:  `Manage integrations with AgentSkills.io compliant AI assistants.`,
	}

	cmd.AddCommand(newSkillInstallCmd())
	return cmd
}

func newSkillInstallCmd() *cobra.Command {
	var harness string
	var local bool

	cmd := &cobra.Command{
		Use:   "install [dir]",
		Short: "Generate an AgentSkills SKILL.md file",
		Long: `Dynamically generates an AgentSkills specification file (SKILL.md) based
on the workers currently registered with the OpenRelik server.

If a directory is provided, the skill will be installed in <dir>/openrelik.
If no directory is provided, it defaults to the global or local directory
configured for the specified harness.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Load workers from cache or API
			workers, err := config.LoadOrRefreshWorkersCache(ctx, func(ctx context.Context) ([]openrelik.Worker, error) {
				client, err := newClient()
				if err != nil {
					return nil, err
				}
				ws, _, err := client.Workers().Registered(ctx)
				return ws, err
			})
			if err != nil {
				return fmt.Errorf("failed to load workers: %w", err)
			}

			baseDir := ""
			if len(args) > 0 {
				baseDir = args[0]
			} else {
				paths, ok := harnesses[harness]
				if !ok {
					return fmt.Errorf("unknown harness: %s", harness)
				}
				if local {
					baseDir = paths.Local
				} else {
					home, err := os.UserHomeDir()
					if err != nil {
						return fmt.Errorf("could not determine home directory: %w", err)
					}
					baseDir = strings.Replace(paths.Global, "~", home, 1)
				}
			}
			destDir := filepath.Join(baseDir, "openrelik")

			// Convert to absolute path for clearer user prompts
			absDestDir, err := filepath.Abs(destDir)
			if err != nil {
				// Fallback to relative path if absolute path fails
				absDestDir = destDir
			}

			// Check if the destination exists and confirm with user
			info, err := os.Stat(destDir)
			if err == nil {
				if info.IsDir() {
					if !quiet {
						fmt.Fprintf(cmd.OutOrStdout(), "Warning: Destination directory %s already exists.\n", absDestDir)
						confirmed, err := util.Confirm(cmd.OutOrStdout(), cmd.InOrStdin(), "Overwrite?")
						if err != nil {
							return err
						}
						if !confirmed {
							fmt.Fprintln(cmd.OutOrStdout(), "Installation cancelled.")
							return nil
						}
					}
				} else {
					return fmt.Errorf("destination %s exists and is not a directory", absDestDir)
				}
			} else if os.IsNotExist(err) {
				if !quiet {
					confirmed, err := util.Confirm(cmd.OutOrStdout(), cmd.InOrStdin(), fmt.Sprintf("Create directory %s and install skill?", absDestDir))
					if err != nil {
						return err
					}
					if !confirmed {
						fmt.Fprintln(cmd.OutOrStdout(), "Installation cancelled.")
						return nil
					}
				}
			} else {
				return err
			}

			if err := GenerateSkillFile(destDir, workers); err != nil {
				return err
			}

			versionFile := filepath.Join(destDir, ".version")
			if err := os.WriteFile(versionFile, []byte(Version+"\n"), 0644); err != nil {
				return fmt.Errorf("failed to write .version file: %w", err)
			}

			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "Successfully generated skill file in %s/SKILL.md\n", absDestDir)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&harness, "harness", "generic", "Target agent framework (generic, codex, gemini, claude, opencode, pi, copilot)")
	cmd.Flags().BoolVar(&local, "local", false, "Install in the local directory according to the harness")

	return cmd
}
