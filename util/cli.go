package util

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// UseArgs returns a PositionalArgs validator derived from the command's Use
// field. It counts required (<ARG>) and optional ([ARG]) tokens to determine
// min/max expected args and names them in any error message.
func UseArgs() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		required, optional, variadic := parseUseArgs(cmd.Use)
		min := len(required)
		max := len(required) + len(optional)
		if variadic {
			max = -1
		}

		n := len(args)
		if n >= min && (max < 0 || n <= max) {
			return nil
		}

		if n < min {
			missing := required[n:]
			if len(missing) == 1 {
				return fmt.Errorf("missing required argument: %s", missing[0])
			}
			return fmt.Errorf("missing required arguments: %s", strings.Join(missing, ", "))
		}

		if max == 0 {
			return fmt.Errorf("unexpected argument: %s", args[0])
		}
		return fmt.Errorf("too many arguments: expected at most %d", max)
	}
}

func parseUseArgs(use string) (required []string, optional []string, variadic bool) {
	for _, token := range strings.Fields(use) {
		if strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">") {
			required = append(required, strings.Trim(token, "<>"))
		} else if strings.HasPrefix(token, "[") && strings.HasSuffix(token, "]") {
			inner := strings.Trim(token, "[]")
			if strings.HasSuffix(inner, "...") {
				variadic = true
			} else {
				optional = append(optional, inner)
			}
		}
	}
	return
}

// SliceArgs splits a slice of arguments by the first found delimiter.
// It returns a slice of argument segments and the delimiter that was used.
// Mixing delimiters is not allowed.
func SliceArgs(firstArg string, args []string, delimiters ...string) ([][]string, string, error) {
	var segments [][]string
	var current []string
	var delimiter string

	current = append(current, firstArg)
	for _, arg := range args {
		isDelimiter := false
		for _, d := range delimiters {
			if arg == d {
				isDelimiter = true
				if delimiter != "" && delimiter != arg {
					return nil, "", fmt.Errorf("cannot mix %s and %s in the same command", delimiter, arg)
				}
				delimiter = arg
				break
			}
		}

		if isDelimiter {
			if len(current) > 0 {
				segments = append(segments, current)
			}
			current = []string{}
		} else {
			current = append(current, arg)
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}

	return segments, delimiter, nil
}
