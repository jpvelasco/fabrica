package destroy

import (
	"fmt"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/prompt"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "destroy",
	Short: "Tear down provisioned infrastructure",
	Long: `Safely dismantle Fabrica-managed infrastructure.

By default, this command shows a summary of what would be destroyed
if --all is provided. It never mutates infrastructure without explicit
confirmation.

Use --all to target all provisioned resources. The command will walk
through a confirmation dialog before proceeding.

Run with --all --yes to skip the interactive prompt (use with care).`,
	RunE: runDestroy,
}

var allFlag bool

func init() {
	Cmd.Flags().BoolVarP(&allFlag, "all", "a", false, "Include all provisioned infrastructure")
}

func runDestroy(cmd *cobra.Command, args []string) error {
	if !allFlag {
		fmt.Println("To destroy infrastructure, use --all:")
		fmt.Println()
		fmt.Println("  fabrica destroy --all")
		fmt.Println()
		fmt.Println("This requires explicit confirmation. Use --all --yes to skip.")
		return nil
	}

	// If we don't have a provider yet, nothing to destroy.
	if globals.Provider == nil {
		fmt.Println("No infrastructure found. Nothing to destroy.")
		return nil
	}

	account, _, region, err := globals.Provider.Identity(cmd.Context())
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}

	// Explain what would happen
	fmt.Println("The following resources will be destroyed:")
	fmt.Println()
	fmt.Printf("  Account: %s\n", account)
	fmt.Printf("  Region:  %s\n", region)
	fmt.Println()
	fmt.Println("  S3 state bucket      : fabrica-state-" + account)
	fmt.Println("  DynamoDB lock table   : fabrica-state-lock")
	fmt.Println()
	fmt.Println("This operation cannot be undone.")

	// Prompt or --yes
	if globals.AssumeYes {
		fmt.Println()
		fmt.Println("Proceeding with --yes...")
	} else {
		fmt.Println()
		if !prompt.Confirm("Continue with destroy?") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Stub — real implementation in Phase 1
	fmt.Println()
	fmt.Println("Stub — actual destruction logic not yet implemented.")
	fmt.Println("This will be wired in Phase 1.")
	fmt.Println()
	fmt.Println("No resources were destroyed.")

	return nil
}
