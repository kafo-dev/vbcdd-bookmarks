// Command bookmarks keeps a WebDAV copy of the browser bookmark tree, the
// file Floccus syncs, reachable from Termux: append a shared link, pick one
// and open it. See the README for the storage contract and the setup.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	version = "0.1.0"

	// The folder the writer owns, as a direct child of <xbel>.
	phoneFolderTitle = "Phone"

	// The subcommand names. An installed name selects one without an
	// argument.
	commandAdd     = "add"
	commandPick    = "pick"
	commandUnlock  = "unlock"
	commandVersion = "version"
	commandHelp    = "help"

	// argv holds the program name, so the subcommand sits one word in.
	commandArg = 1

	exitUsage = 2
)

// Phase ceilings, in one place because each covers a different wait. The
// picker carries none: a reader takes as long as the list needs. Tests
// shorten these to watch one phase outlive another.
var (
	fetchTimeout = 10 * time.Second
	writeTimeout = 30 * time.Second
	openTimeout  = 10 * time.Second

	// toastTimeout bounds the Android toast, which no user waits on.
	toastTimeout = 5 * time.Second
)

// errCancelled reports that the user left the picker without choosing.
var errCancelled = errors.New("canceled")

func main() {
	os.Exit(run(os.Args))
}

// run dispatches one invocation and returns the process exit code.
func run(argv []string) int {
	if len(argv) == 0 {
		return usage("bookmarks: missing command")
	}
	command, args := dispatch(argv)
	switch command {
	case commandAdd:
		return runAdd(args)
	case commandPick:
		return runPick(args)
	case commandUnlock:
		return runUnlock(args)
	case commandVersion:
		if _, err := fmt.Fprintln(os.Stdout, version); err != nil {
			return 1
		}
		return 0
	case commandHelp:
		if err := printUsage(os.Stdout); err != nil {
			return 1
		}
		return 0
	default:
		return usage("bookmarks: unknown command " + command)
	}
}

// dispatch resolves the subcommand. The installed names run the binary
// through a symlink or through `bookmarks <command>`.
func dispatch(argv []string) (string, []string) {
	switch filepath.Base(argv[0]) {
	case "bookmarks-add", "termux-url-opener":
		return commandAdd, argv[commandArg:]
	case "bookmarks-pick":
		return commandPick, argv[commandArg:]
	}
	if len(argv) <= commandArg {
		return "", nil
	}
	switch argv[commandArg] {
	case "-h", "--help", commandHelp:
		return commandHelp, nil
	}
	return argv[commandArg], argv[commandArg+1:]
}

func usage(message string) int {
	if _, err := fmt.Fprintln(os.Stderr, message); err != nil {
		return exitUsage
	}
	if err := printUsage(os.Stderr); err != nil {
		return exitUsage
	}
	return exitUsage
}

func printUsage(w io.Writer) error {
	const text = `usage: bookmarks <command> [flags]

commands:
  add <url>    append a link to the Phone folder of the bookmark file
  pick         list the bookmarks with fzf and open the choice
  unlock       remove a leftover lock file
  version      print the version

The installed names bookmarks-add, bookmarks-pick and termux-url-opener
select a command without an argument.
`
	_, err := fmt.Fprint(w, text)
	return err
}

// report prints the outcome and raises a toast where Termux can: the share
// hook runs with no terminal to print to.
func report(ctx context.Context, message string) {
	if _, err := fmt.Fprintln(os.Stderr, message); err != nil {
		// Nothing remains to report the failure to.
		return
	}
	toast, err := exec.LookPath("termux-toast")
	if err != nil {
		return
	}
	toastCtx, cancel := context.WithTimeout(ctx, toastTimeout)
	defer cancel()
	if err = exec.CommandContext(toastCtx, toast, message).Run(); err != nil {
		// The toast is a convenience; stderr already carries the message.
		return
	}
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "configuration file (default: $XDG_CONFIG_HOME/vbcdd/bookmarks/config.toml)")
}

// openStore loads the configuration and builds the store it names.
func openStore(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = configPath()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, err
	}
	client := NewClient(cfg.URL, cfg.User, cfg.Password)
	return NewStore(cfg, client), nil
}

func runAdd(args []string) int {
	fs := flag.NewFlagSet(commandAdd, flag.ContinueOnError)
	title := fs.String("title", "", "title to store instead of the page title")
	configFile := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()

	if fs.NArg() != 1 {
		return usage("bookmarks add: one URL or one shared text argument")
	}
	shared := fs.Arg(0)
	target, ok := firstURL(shared)
	if !ok {
		report(ctx, "bookmarks: no URL in "+shared)
		return 1
	}
	store, err := openStore(*configFile)
	if err != nil {
		report(ctx, "bookmarks: "+err.Error())
		return 1
	}
	storedTitle := *title
	if storedTitle == "" {
		storedTitle = fetchTitle(ctx, target)
	}
	if storedTitle == "" {
		storedTitle = target
	}
	added, err := appendBookmark(ctx, store, target, storedTitle)
	if err != nil {
		report(ctx, "bookmarks: "+err.Error())
		return 1
	}
	report(ctx, fmt.Sprintf("Saved %q as id %d", added.Title, added.ID))
	return 0
}

// appendBookmark locks the file, appends one link to the phone folder, and
// returns what landed.
func appendBookmark(ctx context.Context, store *Store, target, title string) (*Bookmark, error) {
	var (
		added *Bookmark
		text  string
		doc   *Doc
		err   error
	)
	err = store.WithLock(ctx, func() error {
		if text, err = store.Fetch(ctx); err != nil {
			return err
		}
		if doc, err = ParseDoc(text); err != nil {
			return err
		}
		folder := doc.ChildFolder(phoneFolderTitle)
		if folder == nil {
			if folder, err = doc.NewFolder(phoneFolderTitle, nil); err != nil {
				return err
			}
		}
		if added, err = doc.NewBookmark(folder, target, title); err != nil {
			return err
		}
		return store.Commit(ctx, doc.Render())
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

func runPick(args []string) int {
	fs := flag.NewFlagSet(commandPick, flag.ContinueOnError)
	allowDelete := fs.Bool("delete", false, "let ctrl-d delete the selected bookmark")
	configFile := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	fetchCtx, cancelFetch := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancelFetch()

	store, err := openStore(*configFile)
	if err != nil {
		report(fetchCtx, "bookmarks: "+err.Error())
		return 1
	}
	text, online, err := store.FetchCached(fetchCtx)
	cancelFetch()
	if err != nil {
		report(fetchCtx, "bookmarks: "+err.Error())
		return 1
	}
	doc, err := ParseDoc(text)
	if err != nil {
		report(fetchCtx, "bookmarks: "+err.Error())
		return 1
	}
	entries := collectEntries(doc)
	if len(entries) == 0 {
		report(fetchCtx, "bookmarks: the file holds no bookmark")
		return 1
	}
	header := fmt.Sprintf("%d bookmarks", len(entries))
	if !online {
		header += " (offline copy)"
	}
	chosen, key, err := pick(entries, *allowDelete, header)
	if errors.Is(err, errCancelled) {
		return 0
	}
	if err != nil {
		report(fetchCtx, "bookmarks: "+err.Error())
		return 1
	}
	if key == deleteKey {
		return runDelete(store, chosen)
	}
	if err = openLink(chosen.Href); err != nil {
		report(fetchCtx, "bookmarks: "+err.Error())
		return 1
	}
	return 0
}

// runDelete drops the chosen bookmark through the same lock and write
// protocol the append uses, then reports what left the file.
func runDelete(store *Store, chosen Entry) int {
	if _, err := fmt.Fprintf(os.Stderr, "Delete %q? [y/N] ", chosen.Title); err != nil {
		return 1
	}
	var answer string
	if _, err := fmt.Fscanln(os.Stdin, &answer); err != nil {
		// No answer, no deletion.
		answer = ""
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
		return 0
	}
	// The clock starts after the answer, not before the question.
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := dropBookmark(ctx, store, chosen); err != nil {
		report(ctx, "bookmarks: "+err.Error())
		return 1
	}
	report(ctx, fmt.Sprintf("Deleted %q", chosen.Title))
	return 0
}

// dropBookmark removes one bookmark from the file under the write lock.
func dropBookmark(ctx context.Context, store *Store, chosen Entry) error {
	var (
		text string
		doc  *Doc
		err  error
	)
	err = store.WithLock(ctx, func() error {
		if text, err = store.Fetch(ctx); err != nil {
			return err
		}
		if doc, err = ParseDoc(text); err != nil {
			return err
		}
		node := doc.FindBookmark(chosen.Href, chosen.Title)
		if node == nil {
			return errors.New("the bookmark left the file since the list was read")
		}
		if err = doc.Remove(node); err != nil {
			return err
		}
		return store.Commit(ctx, doc.Render())
	})
	return err
}

func runUnlock(args []string) int {
	fs := flag.NewFlagSet(commandUnlock, flag.ContinueOnError)
	configFile := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	store, err := openStore(*configFile)
	if err != nil {
		report(ctx, "bookmarks: "+err.Error())
		return 1
	}
	state, err := store.LockState(ctx)
	if err != nil {
		report(ctx, "bookmarks: "+err.Error())
		return 1
	}
	if state == LockFree {
		report(ctx, "No lock file to remove")
		return 0
	}
	if err = store.DropLock(ctx); err != nil {
		report(ctx, "bookmarks: "+err.Error())
		return 1
	}
	report(ctx, fmt.Sprintf("Removed the %s lock", state))
	return 0
}

// openLink hands the link to whichever Termux helper this installation has.
// The caller supplies no context: the deadline belongs to this call alone.
func openLink(target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	candidates := [][]string{
		{"termux-open-url", target},
		{"termux-open", "--chooser", target},
		{"termux-am", "start", "-a", "android.intent.action.VIEW", "-d", target},
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}
		if err = exec.CommandContext(ctx, path, candidate[1:]...).Run(); err != nil {
			return fmt.Errorf("%s: %w", candidate[0], err)
		}
		return nil
	}
	return errors.New("no Termux URL opener found; install termux-api or termux-am")
}
