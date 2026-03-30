package main

import (
	"fmt"
	"os"
)

const usage = `Usage: dispatch [command] [options]

Commands:
  (default)    Run due jobs
  list         Show job status table
  run          Force-run a specific job
  run-once     Run a job without DB tracking
  run-all      Force-run all jobs
  reset        Reset a job's next_run to now
  install      Install crontab entry
  uninstall    Remove crontab entry

Options:
  --config     Config file path (default: dispatcher.yaml)
`

func main() {
	args := os.Args[1:]
	configPath := "dispatcher.yaml"

	// Extract --config flag from anywhere in args
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			configPath = args[i+1]
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	args = filtered

	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Print(usage)
		os.Exit(0)
	}

	_ = configPath // used in later tasks

	switch cmd {
	case "":
		fmt.Println("dispatch: no due jobs") // placeholder
	case "list":
		fmt.Println("list: not yet implemented")
	case "run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run <job>")
			os.Exit(1)
		}
		fmt.Printf("run %s: not yet implemented\n", args[0])
	case "run-once":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run-once <job>")
			os.Exit(1)
		}
		fmt.Printf("run-once %s: not yet implemented\n", args[0])
	case "run-all":
		fmt.Println("run-all: not yet implemented")
	case "reset":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch reset <job>")
			os.Exit(1)
		}
		fmt.Printf("reset %s: not yet implemented\n", args[0])
	case "install":
		schedule := "*/5 * * * *"
		if len(args) > 0 {
			schedule = args[0]
		}
		fmt.Printf("install %s: not yet implemented\n", schedule)
	case "uninstall":
		fmt.Println("uninstall: not yet implemented")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
