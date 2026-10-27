package util

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSliceArgs(t *testing.T) {
	tests := []struct {
		name       string
		firstArg   string
		args       []string
		delimiters []string
		want       [][]string
		wantDelim  string
		wantErr    bool
	}{
		{
			name:       "no delimiters",
			firstArg:   "arg1",
			args:       []string{"arg2", "arg3"},
			delimiters: []string{"|", ","},
			want:       [][]string{{"arg1", "arg2", "arg3"}},
			wantDelim:  "",
			wantErr:    false,
		},
		{
			name:       "single delimiter",
			firstArg:   "worker1",
			args:       []string{"arg1", "|", "worker2", "arg2"},
			delimiters: []string{"|"},
			want:       [][]string{{"worker1", "arg1"}, {"worker2", "arg2"}},
			wantDelim:  "|",
			wantErr:    false,
		},
		{
			name:       "multiple occurrences of same delimiter",
			firstArg:   "w1",
			args:       []string{"a1", "|", "w2", "a2", "|", "w3"},
			delimiters: []string{"|", ","},
			want:       [][]string{{"w1", "a1"}, {"w2", "a2"}, {"w3"}},
			wantDelim:  "|",
			wantErr:    false,
		},
		{
			name:       "mixed delimiters error",
			firstArg:   "w1",
			args:       []string{"a1", "|", "w2", ",", "w3"},
			delimiters: []string{"|", ","},
			want:       nil,
			wantDelim:  "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotDelim, err := SliceArgs(tt.firstArg, tt.args, tt.delimiters...)
			if (err != nil) != tt.wantErr {
				t.Errorf("SliceArgs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SliceArgs() got = %v, want %v", got, tt.want)
			}
			if gotDelim != tt.wantDelim {
				t.Errorf("SliceArgs() gotDelim = %v, want %v", gotDelim, tt.wantDelim)
			}
		})
	}
}

func TestUseArgsWithQuoteHint(t *testing.T) {
	tests := []struct {
		name    string
		use     string
		args    []string
		parent  string // value of --parent, if set
		wantErr string // empty means no error
	}{
		{name: "fits", use: "list [FOLDER]", args: []string{"a b"}},
		{name: "no args", use: "list [FOLDER]"},
		{
			name:    "split optional argument",
			use:     "list [FOLDER]",
			args:    []string{"root/This", "is", "a", "subfolder/"},
			wantErr: "too many arguments: expected at most 1, got 4\nQuote FOLDER if it contains spaces: \"root/This is a subfolder/\"",
		},
		{
			name:    "split last of several arguments",
			use:     "rename <ID> <NAME>",
			args:    []string{"42", "New", "name"},
			wantErr: "too many arguments: expected at most 2, got 3\nQuote NAME if it contains spaces: \"New name\"",
		},
		{
			name:    "split may be in a flag value",
			use:     "create <NAME>",
			args:    []string{"logs", "A/"},
			parent:  "Case",
			wantErr: "too many arguments: expected at most 1, got 2\nQuote NAME and the --parent value if they contain spaces.",
		},
		{
			name:    "unset flag keeps the specific hint",
			use:     "create <NAME>",
			args:    []string{"New", "name"},
			wantErr: "Quote NAME if it contains spaces: \"New name\"",
		},
		{name: "missing argument is unchanged", use: "create <NAME>", wantErr: "missing required argument: NAME"},
		{name: "variadic never too many", use: "upload <PATH> [PATH...]", args: []string{"a", "b", "c"}},
		{name: "no positional arguments is unchanged", use: "me", args: []string{"x"}, wantErr: "unexpected argument: x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: tt.use}
			cmd.Flags().String("parent", "", "")
			if tt.parent != "" {
				cmd.Flags().Set("parent", tt.parent)
			}
			err := UseArgsWithQuoteHint("parent")(cmd, tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %v, want %q", err, tt.wantErr)
			}
		})
	}
}
