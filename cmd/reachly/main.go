// Command reachly finds and verifies work emails.
//
//	reachly serve                         web UI + API on :8080
//	reachly find "Jean Dupont" acme.com   one lookup in the terminal
//	reachly bulk people.csv -o out.csv    many lookups in parallel
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ilyasslefhaili/reachly/internal/api"
	"github.com/ilyasslefhaili/reachly/internal/config"
	"github.com/ilyasslefhaili/reachly/internal/domain"
	"github.com/ilyasslefhaili/reachly/internal/finder"
	"github.com/ilyasslefhaili/reachly/internal/jobs"
	"github.com/ilyasslefhaili/reachly/internal/mx"
	"github.com/ilyasslefhaili/reachly/internal/smtpcheck"
	"github.com/ilyasslefhaili/reachly/web"
)

const usage = `Reachly — find a work email from a name + company, verified over SMTP.

Usage:
  reachly serve [flags]                         start the web UI and API (default when no command is given)
  reachly find  <full name> <company|website>   look up one person
  reachly bulk  [file|-] [flags]                look up many people in parallel (one "name, company" per line)

Examples:
  reachly find "Jean Dupont" acme.com
  reachly find Jean Dupont https://www.acme.com --all
  reachly find "Jean Dupont, Acme" --json
  reachly bulk people.csv -o results.csv
  cat people.txt | reachly bulk --format jsonl

Run "reachly <command> -h" for the flags of each command.
Settings can also come from REACHLY_* environment variables (see README).
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	} else if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(usage)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var code int
	switch cmd {
	case "serve":
		code = runServe(ctx, args)
	case "find":
		code = runFind(ctx, args)
	case "bulk":
		code = runBulk(ctx, args)
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}

// commonFlags registers the SMTP settings every command shares; defaults come from env.
func commonFlags(fs *flag.FlagSet, cfg *config.Config) {
	fs.StringVar(&cfg.MailFrom, "mail-from", cfg.MailFrom, "envelope sender for SMTP checks")
	fs.StringVar(&cfg.HELO, "helo", cfg.HELO, "hostname announced in EHLO")
	fs.DurationVar(&cfg.SMTPTimeout, "timeout", cfg.SMTPTimeout, "per-command SMTP timeout")
	fs.IntVar(&cfg.PerDomain, "per-domain", cfg.PerDomain, "max concurrent SMTP sessions per mail server")
}

// parseInterspersed lets flags appear before, between or after positional args.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// flagExit maps a flag parse error to an exit code: -h is success, anything else is usage error.
func flagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

func newFinder(cfg config.Config) *finder.Finder {
	mxr := mx.New()
	ver := &smtpcheck.Verifier{
		HELO: cfg.HELO, MailFrom: cfg.MailFrom, Timeout: cfg.SMTPTimeout,
		ProbeDelay: cfg.ProbeDelay, GreylistWait: cfg.GreylistWait, PerHost: cfg.PerDomain,
	}
	return finder.New(domain.NewHeuristic(mxr), mxr, ver)
}

func runServe(ctx context.Context, args []string) int {
	cfg := config.Load()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.IntVar(&cfg.Workers, "workers", cfg.Workers, "parallel lookups")
	commonFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return flagExit(err)
	}

	q := jobs.New(ctx, jobs.NewMemStore(), newFinder(cfg), cfg.Workers)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           (&api.Server{Q: q, Web: web.FS()}).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		srv.Shutdown(sctx)
	}()
	log.Printf("Reachly listening on http://localhost%s (workers=%d, per-host sessions=%d)", cfg.Addr, cfg.Workers, cfg.PerDomain)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		return 1
	}
	return 0
}
