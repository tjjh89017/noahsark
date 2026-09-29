// Command noahsark is the NoahsArk command-line tool. OPERATIONS.md's CLI
// reference lists its commands, its command groups and its global
// options.
package main

import "os"

func main() {
	os.Exit(run(realEnv(), os.Args[1:]))
}
