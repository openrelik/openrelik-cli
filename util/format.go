package util

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
	"time"
)

// HumanRenderer is implemented by view types that know how to render themselves
// in a minimal, high-signal format suited for interactive terminal use.
type HumanRenderer interface {
	RenderHuman(w io.Writer) error
}

// JSONUnwrapper is implemented by view types that wrap an underlying API struct.
// formatAndPrint calls UnwrapJSON before serialising so the raw struct is emitted,
// not the view wrapper.
type JSONUnwrapper interface {
	UnwrapJSON() any
}

// PrintStruct nicely prints the fields of a struct to stdout.
func PrintStruct(s interface{}) {
	FprintStruct(os.Stdout, s)
}

// FprintStruct nicely prints the fields of a struct or a slice of structs to the given writer.
func FprintStruct(w io.Writer, s interface{}) {
	v := reflect.ValueOf(s)

	// Handle pointer
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			fmt.Fprintf(w, "<nil>\n")
			return
		}
		v = v.Elem()
	}

	// Handle slice
	if v.Kind() == reflect.Slice {
		FprintTable(w, s)
		return
	}

	if v.Kind() != reflect.Struct {
		fmt.Fprintf(w, "%v\n", s)
		return
	}

	FprintPropertyView(w, s)
}

// FprintPropertyView nicely prints a struct's fields vertically as a property list.
// It skips nested structs and slices, and omits nil or empty values.
func FprintPropertyView(w io.Writer, s interface{}) {
	v := reflect.ValueOf(s)

	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			fmt.Fprintf(w, "<nil>\n")
			return
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		fmt.Fprintf(w, "%v\n", s)
		return
	}

	t := v.Type()
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		if !field.IsExported() {
			continue
		}

		// Handle skip logic: nil pointers, slices, and nested structs (except time.Time)
		if value.Kind() == reflect.Ptr && value.IsNil() {
			continue
		}
		if value.Kind() == reflect.Slice {
			continue
		}
		if value.Kind() == reflect.Struct && field.Type != reflect.TypeOf(time.Time{}) {
			continue
		}
		// Skip empty strings
		if value.Kind() == reflect.String && value.String() == "" {
			continue
		}

		var label string
		var val string

		// Custom label mapping for a cleaner UI
		switch field.Name {
		case "ID":
			label = "ID"
		case "DisplayName":
			label = "Display Name"
		case "Filesize":
			label = "Size"
		case "CreatedAt":
			label = "Created"
		case "UpdatedAt":
			label = "Updated"
		case "MagicText":
			label = "Magic Text"
		case "MagicMime":
			label = "Magic Mime"
		case "HashMD5":
			label = "MD5"
		case "HashSHA1":
			label = "SHA1"
		case "HashSHA256":
			label = "SHA256"
		case "HashSSDeep":
			label = "SSDeep"
		case "DataType":
			label = "Type"
		case "UUID":
			label = "UUID"
		case "Filename":
			label = "Filename"
		case "Extension":
			label = "Extension"
		case "UserID":
			label = "User ID"
		case "IsDeleted":
			label = "Deleted"
		case "OriginalPath":
			label = "Original Path"
		case "AuthMethod":
			label = "Auth Method"
		case "IsAdmin":
			label = "Admin"
		case "SpecJSON":
			label = "Spec JSON"
		default:
			label = field.Name
		}

		// Handle field value formatting
		if field.Type == reflect.TypeOf(time.Time{}) {
			tVal := value.Interface().(time.Time)
			if tVal.IsZero() {
				continue
			}
			val = tVal.Format(time.RFC3339) // ISO8601
		} else if label == "Size" && value.Kind() == reflect.Int64 {
			val = FormatBytes(value.Int())
		} else if value.Kind() == reflect.Ptr {
			val = fmt.Sprintf("%v", value.Elem().Interface())
		} else {
			val = fmt.Sprintf("%v", value.Interface())
		}

		if val != "" {
			fmt.Fprintf(tw, "%s\t%s\n", label, val)
		}
	}
	tw.Flush()
}

// FprintTable nicely prints a slice of structs as a table to the given writer.
func FprintTable(w io.Writer, s interface{}) {
	v := reflect.ValueOf(s)

	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Slice {
		FprintStruct(w, s)
		return
	}

	if v.Len() == 0 {
		return
	}

	// Determine columns by inspecting the first element
	elem := v.Index(0)
	if elem.Kind() == reflect.Ptr {
		if elem.IsNil() {
			return
		}
		elem = elem.Elem()
	}

	if elem.Kind() != reflect.Struct {
		// Fallback for non-struct slices
		for i := 0; i < v.Len(); i++ {
			fmt.Fprintln(w, v.Index(i).Interface())
		}
		return
	}

	t := elem.Type()
	var cols []int
	var headers []string

	// Identify identifier
	if f, ok := t.FieldByName("ID"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "ID")
	}

	// Identify title/name
	if f, ok := t.FieldByName("DisplayName"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "DISPLAY NAME")
	} else if f, ok := t.FieldByName("TaskName"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "TASK NAME")
	} else if f, ok := t.FieldByName("Name"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "NAME")
	}

	// Identify metadata
	if f, ok := t.FieldByName("Filesize"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "SIZE")
	}
	if f, ok := t.FieldByName("DataType"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "TYPE")
	}
	if f, ok := t.FieldByName("Description"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "DESCRIPTION")
	}
	if f, ok := t.FieldByName("StatusShort"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "STATUS")
	}

	// Identify timestamp
	if f, ok := t.FieldByName("UpdatedAt"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "UPDATED")
	} else if f, ok := t.FieldByName("CreatedAt"); ok {
		cols = append(cols, f.Index[0])
		headers = append(headers, "CREATED")
	}

	// Fallback if no known columns found
	if len(cols) == 0 {
		for i := 0; i < t.NumField() && len(cols) < 4; i++ {
			f := t.Field(i)
			if f.IsExported() && (f.Type.Kind() == reflect.String || f.Type.Kind() == reflect.Int) {
				cols = append(cols, i)
				headers = append(headers, strings.ToUpper(f.Name))
			}
		}
	}

	if len(cols) == 0 {
		// Fallback to struct printing if no suitable fields found
		for i := 0; i < v.Len(); i++ {
			FprintStruct(w, v.Index(i).Interface())
			if i < v.Len()-1 {
				fmt.Fprintln(w, "---")
			}
		}
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Print headers
	for i, h := range headers {
		fmt.Fprint(tw, h)
		if i < len(headers)-1 {
			fmt.Fprint(tw, "\t")
		}
	}
	fmt.Fprintln(tw)

	// Print rows
	for i := 0; i < v.Len(); i++ {
		val := v.Index(i)
		if val.Kind() == reflect.Ptr {
			if val.IsNil() {
				continue
			}
			val = val.Elem()
		}

		for j, colIdx := range cols {
			fieldVal := val.Field(colIdx)
			var str string

			if fieldVal.Type() == reflect.TypeOf(time.Time{}) {
				str = FormatTimeAgo(fieldVal.Interface().(time.Time))
			} else if headers[j] == "SIZE" && fieldVal.Kind() == reflect.Int64 {
				str = FormatBytes(fieldVal.Int())
			} else if fieldVal.Kind() == reflect.Ptr {
				if fieldVal.IsNil() {
					str = ""
				} else {
					str = fmt.Sprintf("%v", fieldVal.Elem().Interface())
				}
			} else {
				str = fmt.Sprintf("%v", fieldVal.Interface())
			}

			if headers[j] == "DESCRIPTION" && len(str) > 60 {
				str = str[:57] + "..."
			}

			fmt.Fprint(tw, str)
			if j < len(cols)-1 {
				fmt.Fprint(tw, "\t")
			}
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()
}

func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatTimeAgo formats a time.Time into a relative string like "about 15 days ago".
func FormatTimeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		m := int(d.Minutes())
		if m == 1 {
			return "about 1 minute ago"
		}
		return fmt.Sprintf("about %d minutes ago", m)
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		if h == 1 {
			return "about 1 hour ago"
		}
		return fmt.Sprintf("about %d hours ago", h)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "about 1 day ago"
	}
	if days < 30 {
		return fmt.Sprintf("about %d days ago", days)
	}
	months := days / 30
	if months == 1 {
		return "about 1 month ago"
	}
	if months < 12 {
		return fmt.Sprintf("about %d months ago", months)
	}
	years := months / 12
	if years == 1 {
		return "about 1 year ago"
	}
	return fmt.Sprintf("about %d years ago", years)
}

// FprintJSON prints the given interface as a pretty-printed JSON string.
func FprintJSON(w io.Writer, s interface{}) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(s)
}
