package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/api"
	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/packet"
	"github.com/dennisdevulder/ltm/internal/store"
)

func newServerCmd() *cobra.Command {
	var dbPath string
	var addr string

	c := &cobra.Command{
		Use:   "server",
		Short: "Run or initialize the ltm HTTP server.",
		Long:  "Run the ltm server. Use 'ltm server init' before first run to create the database and a root token.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()

			// refuse to start without any auth tokens
			any, err := s.AnyToken(cmd.Context())
			if err != nil {
				return err
			}
			if !any {
				return fmt.Errorf("no auth tokens in database. run 'ltm server init --db %s' first", dbPath)
			}

			srv := &http.Server{
				Addr:              addr,
				Handler:           api.New(s, log.Default()).Handler(),
				ReadHeaderTimeout: 10 * time.Second,
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				log.Printf("ltm server listening on %s (db: %s)", addr, dbPath)
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errCh <- err
				}
			}()

			select {
			case <-ctx.Done():
				log.Println("shutdown requested")
			case err := <-errCh:
				return err
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		},
	}
	c.Flags().StringVar(&dbPath, "db", defaultDBPath(), "SQLite database file")
	c.Flags().StringVar(&addr, "addr", ":8080", "listen address")

	init := &cobra.Command{
		Use:   "init",
		Short: "Initialize a server: create the database and generate a root token.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
				return err
			}
			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()

			tok := packet.RandomToken()
			if err := s.PutTokenHash(cmd.Context(), auth.HashToken(tok), "root"); err != nil {
				return err
			}

			fmt.Fprintln(os.Stderr, "ltm server initialized.")
			fmt.Fprintln(os.Stderr, "database:", dbPath)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "root token (shown once — copy it now):")
			fmt.Fprintln(os.Stderr, "")
			fmt.Println(tok)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "authenticate a client with:")
			fmt.Fprintf(os.Stderr, "  ltm auth http://<this-host>:8080 %s\n", tok)
			return nil
		},
	}
	init.Flags().StringVar(&dbPath, "db", defaultDBPath(), "SQLite database file")
	c.AddCommand(init)

	issue := &cobra.Command{
		Use:   "issue-token <label>",
		Short: "Issue a new bearer token.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()
			tok := packet.RandomToken()
			if err := s.PutTokenHash(cmd.Context(), auth.HashToken(tok), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "issued token for %q (shown once):\n", args[0])
			fmt.Println(tok)
			return nil
		},
	}
	issue.Flags().StringVar(&dbPath, "db", defaultDBPath(), "SQLite database file")
	c.AddCommand(issue)

	return c
}

func defaultDBPath() string {
	if v := os.Getenv("LTM_DB"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "ltm.db"
	}
	return filepath.Join(home, ".local", "share", "ltm", "ltm.db")
}
