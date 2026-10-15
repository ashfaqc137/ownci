package cli

import "github.com/spf13/cobra"

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that this machine can run OwnCI (platform, Docker, dependencies)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return ErrNotImplemented
		},
	}
}
