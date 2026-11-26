package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/registry"
)

// completeInstance is the shell completion of a command whose only positional argument is an
// instance: it offers the names `den ls` prints.
//
// It reads den's records and never calls sbx. A completion runs at every <TAB>: `sbx ls` takes long
// enough to be felt, and fails when sbx is not signed in, while a name needs no VM state. An
// unreadable record is skipped without a word, because a completion has nowhere to print a warning;
// `den ls` is the command that names it.
//
// Every return carries ShellCompDirectiveNoFileComp: an instance is never a path, and without it the
// shell falls back to the files of the current folder, which are all wrong answers here.
func completeInstance(denHome *string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		home, err := config.Home(*denHome)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		records, _, err := registry.List(home)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, r := range records {
			if strings.HasPrefix(r.Instance, toComplete) {
				names = append(names, r.Instance)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
