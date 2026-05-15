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
	"path/filepath"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

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
	cmd := &cobra.Command{
		Use:   "install [dir]",
		Short: "Generate an AgentSkills SKILL.md file",
		Long: `Dynamically generates an AgentSkills specification file (SKILL.md) based
on the workers currently registered with the OpenRelik server.

If a directory is provided, the skill will be installed in <dir>/openrelik.
If no directory is provided, it defaults to '.agents/skills/openrelik' in the
current working directory.`,
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

			baseDir := ".agents/skills"
			if len(args) > 0 {
				baseDir = args[0]
			}
			destDir := filepath.Join(baseDir, "openrelik")

			if err := GenerateSkillFile(destDir, workers); err != nil {
				return err
			}

			fmt.Printf("Successfully generated skill file in %s/SKILL.md\n", destDir)
			return nil
		},
	}

	return cmd
}
