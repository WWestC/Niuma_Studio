package recruit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RunDrive is the `niuma drive` subcommand: launch one new
// GUI-native session by injecting the brief (D2t). Exit codes: 0
// launched (or dry run), 1 failure, 3 the no-window precondition.
func RunDrive(args []string) int {
	var brief string
	dry := false
	attempts := 3
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--brief":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "drive: --brief needs a path")
				return 2
			}
			i++
			brief = args[i]
		case "--dry-run":
			dry = true
		case "--attempts":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "drive: --attempts needs a number")
				return 2
			}
			i++
			if _, err := fmt.Sscanf(args[i], "%d", &attempts); err != nil || attempts < 1 {
				fmt.Fprintln(os.Stderr, "drive: --attempts must be a positive number")
				return 2
			}
		case "-h", "--help":
			fmt.Fprint(os.Stdout, `usage: niuma drive --brief PATH [--dry-run] [--attempts N]

Launch a new GUI-native ZCode session by injecting the brief through
the clipboard and the app's own 新建任务 menu (C1-hardened). The
operator's clipboard is backed up and restored. --dry-run prints the
exact human-runnable sequence with the brief's content withheld.
`)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "drive: unknown flag %q\n", args[i])
			return 2
		}
	}
	if brief == "" {
		fmt.Fprintln(os.Stderr, "drive: --brief PATH is required")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "drive:", err)
		return 1
	}
	d := &Driver{
		AppName:   "ZCode",
		BriefPath: brief,
		TaskDB:    filepath.Join(home, ".zcode", "v2", "tasks-index.sqlite"),
		Attempts:  attempts,
		DryRun:    dry,
		Printf:    func(format string, args ...any) { fmt.Printf(format, args...) },
	}
	if err := d.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, ErrNoWindow) {
			return 3
		}
		return 1
	}
	return 0
}
