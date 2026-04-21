package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/config"
)

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Read and write CLI configuration.",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Set a config value.",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				if err := cfg.Set(args[0], args[1]); err != nil {
					return err
				}
				return cfg.Save()
			},
		},
		&cobra.Command{
			Use:   "get <key>",
			Short: "Print a config value.",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				cfg.Resolve()
				v, err := cfg.Get(args[0])
				if err != nil {
					return err
				}
				fmt.Println(v)
				return nil
			},
		},
		&cobra.Command{
			Use:   "unset <key>",
			Short: "Remove a config value.",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				if err := cfg.Unset(args[0]); err != nil {
					return err
				}
				return cfg.Save()
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "Print the whole config.",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				cfg.Resolve()
				for _, kv := range cfg.All() {
					fmt.Printf("%s = %s\n", kv[0], kv[1])
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open the config file in $EDITOR.",
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := config.Path()
				if err != nil {
					return err
				}
				cfg, _ := config.Load()
				editor := cfg.Editor
				if editor == "" {
					editor = os.Getenv("EDITOR")
				}
				if editor == "" {
					editor = "vi"
				}
				c := exec.Command(editor, p)
				c.Stdin = os.Stdin
				c.Stdout = os.Stdout
				c.Stderr = os.Stderr
				return c.Run()
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print the path of the config file.",
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := config.Path()
				if err != nil {
					return err
				}
				fmt.Println(p)
				return nil
			},
		},
	)
	return c
}
