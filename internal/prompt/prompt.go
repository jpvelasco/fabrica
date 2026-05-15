// Package prompt provides a simple tty-aware confirmation prompt.
package prompt

import (
	"bufio"
	"fmt"
	"os"
)

// Confirm asks the user a yes/no question. Returns true if the user
// types "y" or "yes" (any case), false otherwise. On Windows or other
// non-tty environments the prompt is still printed and stdin is read
// — the caller should use --yes to skip the prompt in non-interactive
// environments.
func Confirm(msg string) bool {
	fmt.Printf("%s [y/N]: ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		fmt.Println()
		return false
	}
	answer := scanner.Text()
	fmt.Println()
	return answer == "y" || answer == "Y" || answer == "yes" || answer == "Yes" || answer == "YES"
}
