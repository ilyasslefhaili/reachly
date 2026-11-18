package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/ilyasslefhaili/reachly/internal/config"
	"github.com/ilyasslefhaili/reachly/internal/finder"
)

const lookupTimeout = 90 * time.Second

// ---------- find ----------

func runFind(ctx context.Context, args []string) int {
	cfg := config.Load()
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the full result as JSON")
	all := fs.Bool("all", false, "show every pattern that was checked")
	commonFlags(fs, &cfg)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: reachly find <full name> <company|website> [--all] [--json]\n\n")
		fs.PrintDefaults()
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return flagExit(err)
	}
	name, company, err := findArgs(pos)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fs.Usage()
		return 2
	}

	stop := spinner(fmt.Sprintf("Looking up %s at %s", name, company))
	lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	res, err := newFinder(cfg).Find(lctx, name, company)
	cancel()
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, red("error: ")+err.Error())
		return 1
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(res)
	} else {
		printResult(res, *all)
	}
	if res.Best == nil {
		return 1
	}
	return 0
}

// findArgs accepts `"Jean Dupont" acme.com`, `Jean Dupont acme.com` or `"Jean Dupont, acme.com"`.
func findArgs(pos []string) (string, string, error) {
	switch len(pos) {
	case 0:
		return "", "", errors.New("missing name and company")
	case 1:
		return finder.ParseLine(pos[0])
	default:
		return strings.Join(pos[:len(pos)-1], " "), pos[len(pos)-1], nil
	}
}

func printResult(r finder.Result, all bool) {
	if b := r.Best; b != nil {
		fmt.Printf("\n  %s  %s  %d%% confidence\n", bold(b.Email), statusLabel(string(b.Status)), b.Confidence)
	} else {
		fmt.Printf("\n  %s every pattern was rejected by the mail server\n", red("✘ no email found:"))
	}
	fmt.Printf("  %s %s · %s · %s\n", dim("domain   "), r.Domain, r.Provider, r.MXHost)
	fmt.Printf("  %s %s · %.1fs\n", dim("catch-all"), r.CatchAll, float64(r.DurationMS)/1000)
	if r.CatchAll == "yes" {
		fmt.Println(yellow("  ! This domain accepts every address, so SMTP can't confirm the mailbox. Showing the most common pattern."))
	}
	if r.Note != "" {
		fmt.Println(yellow("  ! " + r.Note))
	}
	if all {
		fmt.Println()
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  EMAIL\tPATTERN\tSTATUS\tCONF\tSMTP REPLY")
		for _, c := range r.Candidates {
			msg := c.SMTPMsg
			if len(msg) > 60 {
				msg = msg[:60] + "…"
			}
			code := ""
			if c.SMTPCode != 0 {
				code = strconv.Itoa(c.SMTPCode) + " "
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%d%%\t%s\n", c.Email, c.Pattern, c.Status, c.Confidence, code+msg)
		}
		tw.Flush()
	}
	fmt.Println()
}

// ---------- bulk ----------

type row struct {
	idx           int
	name, company string
	res           finder.Result
	err           error
}

func runBulk(ctx context.Context, args []string) int {
	cfg := config.Load()
	fs := flag.NewFlagSet("bulk", flag.ContinueOnError)
	out := fs.String("o", "", "write results to this file instead of stdout")
	format := fs.String("format", "csv", "output format: csv or jsonl")
	workers := fs.Int("workers", cfg.Workers, "parallel lookups")
	commonFlags(fs, &cfg)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: reachly bulk [file|-] [-o out.csv] [--format csv|jsonl] [--workers N]\n\nInput: one \"full name, company or website\" per line (a header line is skipped).\n\n")
		fs.PrintDefaults()
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if *format != "csv" && *format != "jsonl" {
		fmt.Fprintln(os.Stderr, "error: --format must be csv or jsonl")
		return 2
	}

	in, err := openInput(pos)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fs.Usage()
		return 2
	}
	defer in.Close()
	rows, err := readRows(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "error: no input lines")
		return 2
	}

	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		defer f.Close()
		w = f
	}
	emit := newEmitter(w, *format)

	// Worker pool: goroutines pull rows and push finished ones to `done`.
	f := newFinder(cfg)
	todo := make(chan row)
	done := make(chan row)
	var wg sync.WaitGroup
	for range max(1, *workers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range todo {
				if r.err == nil {
					lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
					r.res, r.err = f.Find(lctx, r.name, r.company)
					cancel()
				}
				done <- r
			}
		}()
	}
	go func() {
		defer close(todo)
		for _, r := range rows {
			select {
			case todo <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(done) }()

	// Write results in input order as soon as each next row is ready.
	start := time.Now()
	pending := map[int]row{}
	next, finished, found := 0, 0, 0
	for r := range done {
		finished++
		if r.err == nil && r.res.Best != nil {
			found++
		}
		progress(finished, len(rows), r)
		pending[r.idx] = r
		for {
			p, ok := pending[next]
			if !ok {
				break
			}
			emit(p)
			delete(pending, next)
			next++
		}
	}
	// After an interrupt, write any finished rows that were waiting on a gap.
	for i := next; len(pending) > 0 && i < len(rows); i++ {
		if p, ok := pending[i]; ok {
			emit(p)
			delete(pending, i)
		}
	}
	emit(row{idx: -1}) // flush
	fmt.Fprintf(os.Stderr, "\n%s %d/%d with an email · %.1fs", green("done:"), found, len(rows), time.Since(start).Seconds())
	if *out != "" {
		fmt.Fprintf(os.Stderr, " · saved to %s", *out)
	}
	fmt.Fprintln(os.Stderr)
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, yellow("interrupted: results above are partial"))
		return 130
	}
	return 0
}

func openInput(pos []string) (io.ReadCloser, error) {
	if len(pos) == 0 || pos[0] == "-" {
		if st, _ := os.Stdin.Stat(); len(pos) == 0 && st.Mode()&os.ModeCharDevice != 0 {
			return nil, errors.New("give an input file, or pipe lines into stdin")
		}
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(pos[0])
}

func readRows(in io.Reader) ([]row, error) {
	var rows []row
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first && finder.IsHeader(line) {
			first = false
			continue
		}
		first = false
		name, company, err := finder.ParseLine(line)
		rows = append(rows, row{idx: len(rows), name: name, company: company, err: err})
	}
	return rows, sc.Err()
}

// newEmitter returns a function that writes one row; idx -1 flushes.
func newEmitter(w io.Writer, format string) func(row) {
	if format == "jsonl" {
		enc := json.NewEncoder(w)
		return func(r row) {
			if r.idx < 0 {
				return
			}
			o := map[string]any{"name": r.name, "company": r.company}
			if r.err != nil {
				o["error"] = r.err.Error()
			} else {
				o["result"] = r.res
			}
			enc.Encode(o)
		}
	}
	cw := csv.NewWriter(w)
	cw.Write([]string{"name", "company", "email", "status", "confidence", "domain", "provider", "catch_all", "note", "error"})
	return func(r row) {
		if r.idx < 0 {
			cw.Flush()
			return
		}
		rec := []string{r.name, r.company, "", "", "", "", "", "", "", ""}
		if r.err != nil {
			rec[9] = r.err.Error()
		} else {
			if b := r.res.Best; b != nil {
				rec[2], rec[3], rec[4] = b.Email, string(b.Status), strconv.Itoa(b.Confidence)
			} else {
				rec[3] = "not_found"
			}
			rec[5], rec[6], rec[7], rec[8] = r.res.Domain, r.res.Provider, r.res.CatchAll, r.res.Note
		}
		cw.Write(rec)
		cw.Flush() // stream each row so partial results survive Ctrl-C
	}
}

func progress(n, total int, r row) {
	label := ""
	switch {
	case r.err != nil:
		label = red("error: " + r.err.Error())
	case r.res.Best == nil:
		label = red("not found")
	default:
		label = r.res.Best.Email + " " + statusLabel(string(r.res.Best.Status))
	}
	fmt.Fprintf(os.Stderr, "%s %s, %s → %s\n", dim(fmt.Sprintf("[%d/%d]", n, total)), r.name, r.company, label)
}

// ---------- terminal helpers ----------

var color = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}()

func paint(code, s string) string {
	if !color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func bold(s string) string   { return paint("1", s) }
func dim(s string) string    { return paint("2", s) }
func red(s string) string    { return paint("31", s) }
func green(s string) string  { return paint("32", s) }
func yellow(s string) string { return paint("33", s) }

func statusLabel(s string) string {
	switch s {
	case "valid":
		return green("✔ valid")
	case "catch_all":
		return yellow("~ catch-all (best guess)")
	case "invalid":
		return red("✘ invalid")
	default:
		return dim("? unknown")
	}
}

// spinner shows progress on stderr while a single lookup runs; call the returned func to stop.
func spinner(msg string) func() {
	if !color {
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(os.Stderr, "\r%s %s…", frames[i%len(frames)], msg)
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\x1b[K")
				return
			case <-t.C:
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}
