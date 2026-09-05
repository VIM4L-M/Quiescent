package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/VIM4L-M/Quiescent/internal/migrations"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Create or update the database tables",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate()
		},
	}
}

func runMigrate() error {
	source, err := iofs.New(migrations.Files, "files")
	if err != nil {
		return err
	}
	dbURL := envString("DB_URL", "")
	m, err := migrate.NewWithSourceInstance("iofs", source, pgx5URL(dbURL))
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	fmt.Println(green(bold("migrations applied")))
	return nil
}

func pgx5URL(dbURL string) string {
	return "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(dbURL, "postgres://"), "postgresql://")
}
