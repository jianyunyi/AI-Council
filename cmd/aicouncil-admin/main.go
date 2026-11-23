package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/aicouncil/aicouncil/internal/backup"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: aicouncil-admin backup|restore [flags]")
	}
	key, e := hex.DecodeString(os.Getenv("AICOUNCIL_BACKUP_KEY"))
	if e != nil || len(key) != 32 {
		return errors.New("AICOUNCIL_BACKUP_KEY must be 64 hex characters")
	}
	switch args[0] {
	case "backup":
		f := flag.NewFlagSet("backup", flag.ContinueOnError)
		db := f.String("db", ".data/council.db", "SQLite database")
		runnerDB := f.String("runner-db", "", "Runner SQLite database (optional)")
		art := f.String("artifacts", ".data/artifacts", "artifact directory")
		out := f.String("out", "", "new encrypted archive path")
		if e = f.Parse(args[1:]); e != nil {
			return e
		}
		if *out == "" {
			return errors.New("--out is required")
		}
		return backup.CreateWithRunner(*db, *runnerDB, *art, *out, key)
	case "restore":
		f := flag.NewFlagSet("restore", flag.ContinueOnError)
		in := f.String("in", "", "encrypted archive")
		dest := f.String("dest", "", "new isolated directory")
		if e = f.Parse(args[1:]); e != nil {
			return e
		}
		return backup.Restore(*in, *dest, key)
	default:
		return errors.New("usage: aicouncil-admin backup|restore [flags]")
	}
}
