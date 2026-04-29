// Command disco is a cloud-resource discovery CLI — see CLAUDE.md and
// the package docs under cmd/ for usage. main only dispatches to cmd.
package main

import "codeberg.org/icearp/disco/cmd"

func main() {
	cmd.Execute()
}
