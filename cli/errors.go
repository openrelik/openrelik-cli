package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

// errorsWrapped is the annotation of commands that wrapErrors has already
// handled, such as the worker commands that 'run' adds while running.
const errorsWrapped = "openrelik:errors-wrapped"

// wrapErrors applies markUsageErrors and friendlyErrors to every command in
// the tree, and marks them so they aren't wrapped twice.
func wrapErrors(cmd *cobra.Command) {
	markUsageErrors(cmd)
	friendlyErrors(cmd)
	markErrorsWrapped(cmd)
}

func markErrorsWrapped(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[errorsWrapped] = "true"
	for _, c := range cmd.Commands() {
		markErrorsWrapped(c)
	}
}

// friendlyErrors makes every command in the tree describe failed API
// requests in plain words, e.g. "file 999999 not found" instead of
// "openrelik: GET http://…/api/v1/files/999999: 404 Not Found: File not
// found.". The raw form is kept with --debug.
func friendlyErrors(cmd *cobra.Command) {
	if cmd.Annotations[errorsWrapped] != "" {
		return
	}
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			err := run(cmd, args)
			if err == nil || debug {
				return err
			}
			interrupted := cmd.Context() != nil && cmd.Context().Err() != nil
			return explainError(err, interrupted, apiKeyHint(cmd))
		}
	}
	for _, c := range cmd.Commands() {
		friendlyErrors(c)
	}
}

// explainedError replaces the message of a failed request in err, keeping
// the rest of the message and the error chain.
type explainedError struct {
	msg string
	err error
}

func (e *explainedError) Error() string { return e.msg }
func (e *explainedError) Unwrap() error { return e.err }

// explainError rewrites the part of err's message that comes from the API
// client or the network. Errors that don't come from a request are returned
// unchanged. If interrupted, a request that was cut short says so. keyHint
// is added when the server doesn't accept the API key.
func explainError(err error, interrupted bool, keyHint string) error {
	var raw, plain string
	var apiErr *openrelik.Error
	var urlErr *url.Error
	switch {
	case errors.As(err, &apiErr):
		raw, plain = apiErr.Error(), explainAPIError(apiErr, keyHint)
	case errors.As(err, &urlErr):
		raw, plain = urlErr.Error(), explainURLError(urlErr, interrupted, keyHint)
	default:
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, raw) {
		return err
	}
	msg = strings.Replace(msg, raw, plain, 1)
	// "file 7: file 7 not found" says it twice.
	if what, ok := strings.CutSuffix(plain, " not found"); ok {
		msg = strings.Replace(msg, what+": "+plain, plain, 1)
	}
	return &explainedError{msg: msg, err: err}
}

// resourcePattern finds the last "<resource>/<ID>" in an API path, e.g.
// "workflows/7" in /api/v1/folders/3/workflows/7/status/.
var resourcePattern = regexp.MustCompile(`([a-z]+)s/(\d+)(?:/[a-z]*/?)?$`)

// apiKeyHint says what to do about an API key the server doesn't accept.
func apiKeyHint(cmd *cobra.Command) string {
	switch {
	case cmd.Name() == "login":
		return ""
	case resolveCredentials().APIKeySource == "OPENRELIK_API_KEY":
		return "; check OPENRELIK_API_KEY"
	default:
		return "; log in again with 'openrelik auth login'"
	}
}

func explainAPIError(e *openrelik.Error, keyHint string) string {
	status := e.StatusCode
	detail := strings.TrimSuffix(e.Message, ".")
	switch {
	case status == 0 || status < 400:
		if e.Cause != nil {
			return fmt.Sprintf("unexpected response from the server: %v", e.Cause)
		}
	case status == http.StatusUnauthorized:
		return "the server didn't accept the API key" + keyHint
	case status == http.StatusForbidden:
		if detail != "" {
			return "permission denied: " + detail
		}
		return "permission denied"
	case status == http.StatusNotFound:
		if e.Response != nil && e.Response.Request != nil {
			if m := resourcePattern.FindStringSubmatch(e.Response.Request.URL.Path); m != nil {
				return fmt.Sprintf("%s %s not found", m[1], m[2])
			}
		}
		if detail != "" {
			return "not found: " + detail
		}
		return "not found"
	case status >= 500:
		msg := fmt.Sprintf("the server failed to handle the request (%d %s)", status, http.StatusText(status))
		if detail != "" {
			msg += ": " + detail
		}
		return msg
	}
	if detail != "" {
		return fmt.Sprintf("%s (%d %s)", detail, status, http.StatusText(status))
	}
	return fmt.Sprintf("request failed (%d %s)", status, http.StatusText(status))
}

func explainURLError(e *url.Error, interrupted bool, keyHint string) string {
	server := e.URL
	if u, err := url.Parse(e.URL); err == nil {
		server = u.Scheme + "://" + u.Host
	}
	if interrupted {
		return fmt.Sprintf("the request to %s was stopped", server)
	}
	cause := e.Err
	// The client asks for a new access token when a request is refused;
	// it only reports why that failed in the message.
	if status, ok := strings.CutPrefix(cause.Error(), "openrelik: failed to refresh token: "); ok {
		if strings.HasPrefix(status, "401") || strings.HasPrefix(status, "403") {
			return "the server didn't accept the API key" + keyHint
		}
		return fmt.Sprintf("could not log in to %s (%s)", server, status)
	}
	var opErr *net.OpError
	if errors.As(cause, &opErr) {
		cause = opErr.Err
	}
	var sysErr *os.SyscallError
	if errors.As(cause, &sysErr) {
		cause = sysErr.Err
	}
	var dnsErr *net.DNSError
	if errors.As(cause, &dnsErr) {
		return fmt.Sprintf("could not reach %s: unknown host %s", server, dnsErr.Name)
	}
	if e.Timeout() {
		return fmt.Sprintf("could not reach %s: timed out", server)
	}
	return fmt.Sprintf("could not reach %s: %v", server, cause)
}

// Exit codes, so scripts can tell kinds of failure apart. Documented in
// README.md; don't change their meaning.
const (
	exitError       = 1   // any other error, including a failed workflow
	exitUsage       = 2   // the command line is wrong
	exitNotFound    = 3   // a file, folder, workflow or other resource doesn't exist
	exitAuth        = 4   // no credentials, or the server refused them or the request
	exitUnavailable = 5   // the server can't be reached or failed; worth retrying
	exitInterrupted = 130 // Ctrl-C, as shells report it
)

// usageError is an error in the command line: an unknown command or flag,
// wrong arguments, or an invalid value.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usageErrorf(format string, a ...any) error {
	return &usageError{fmt.Errorf(format, a...)}
}

// credentialsError is returned when no server or API key is set.
type credentialsError struct{ msg string }

func (e *credentialsError) Error() string { return e.msg }

// markUsageErrors makes argument and flag errors of every command in the
// tree usage errors.
func markUsageErrors(cmd *cobra.Command) {
	if cmd.Annotations[errorsWrapped] != "" {
		return
	}
	if args := cmd.Args; args != nil {
		cmd.Args = func(cmd *cobra.Command, a []string) error {
			err := args(cmd, a)
			if err == nil || errors.As(err, new(*usageError)) {
				return err
			}
			return &usageError{err}
		}
	}
	for _, c := range cmd.Commands() {
		markUsageErrors(c)
	}
}

// cobraUsageErrors start the messages of command-line errors that Cobra and
// pflag report without going through our hooks, and of the errors from
// parsing worker flags in util.BuildWorkflowSpec.
var cobraUsageErrors = []string{
	"failed to parse flags for worker",
	"unknown worker",
	"required flag(s)",
	"if any flags in the group",
	"unknown flag",
	"unknown shorthand flag",
	"invalid argument",
	"flag needs an argument",
	"bad flag syntax",
}

// exitCode returns the exit status for an error returned by a command.
func exitCode(err error) int {
	var usage *usageError
	var input *util.InputError
	var notFound notFoundError
	var creds *credentialsError
	var apiErr *openrelik.Error
	var urlErr *url.Error
	switch {
	case errors.As(err, &usage), errors.As(err, &input):
		return exitUsage
	case errors.As(err, &notFound):
		return exitNotFound
	case errors.As(err, &creds):
		return exitAuth
	case errors.As(err, &apiErr):
		switch s := apiErr.StatusCode; {
		case s == http.StatusNotFound:
			return exitNotFound
		case s == http.StatusUnauthorized || s == http.StatusForbidden:
			return exitAuth
		case s >= 500:
			return exitUnavailable
		}
	case errors.As(err, &urlErr):
		if status, ok := strings.CutPrefix(urlErr.Err.Error(), "openrelik: failed to refresh token: "); ok {
			if strings.HasPrefix(status, "401") || strings.HasPrefix(status, "403") {
				return exitAuth
			}
		}
		return exitUnavailable
	}
	// Cobra checks these itself, before any of our code runs, and flags
	// before the subcommand are parsed without the flag error function.
	for _, prefix := range cobraUsageErrors {
		if strings.HasPrefix(err.Error(), prefix) {
			return exitUsage
		}
	}
	return exitError
}
