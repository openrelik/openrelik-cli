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
	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/spf13/cobra"
)

func newWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Manage workers",
		Long:  `Inspect workers registered with OpenRelik.`,
	}

	cmd.AddCommand(newListWorkersCmd())
	return cmd
}

func newListWorkersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered workers",
		Long: `List all workers currently registered with the OpenRelik server.

This command also refreshes the local worker cache used to generate
'run' subcommands. Run this after new workers are deployed to pick them up.`,
		Example: `  # List all registered workers
  openrelik worker list

  # Output as JSON
  openrelik worker list --format json`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}

			workers, _, err := client.Workers().Registered(cmd.Context())
			if err != nil {
				return err
			}

			if err := config.SaveWorkersCache(workers); err != nil {
				return err
			}

			return formatAndPrint(cmd, &WorkerListView{Workers: workers})
		},
	}

	return cmd
}
