package util

import (
	"reflect"
	"testing"
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
