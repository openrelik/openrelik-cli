package util

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	ColorReset  = "\033[0m"
	ColorDim    = "\033[2m"
	ColorBold   = "\033[1m"
	ColorBlue   = "\033[94m"
	ColorGreen  = "\033[32m"
	ColorRed    = "\033[31m"
	ColorYellow = "\033[33m"
	ColorCyan   = "\033[36m"
)

// ProgressTracker handles the display of a progress bar.
type ProgressTracker struct {
	TotalBytes  int64
	TotalChunks int
	Action      string
	Writer      io.Writer

	currentBytes  int64
	currentChunks int
	retries       int
	startTime     time.Time
	samples       []progressSample

	lastChunkTime  time.Time
	chunkDurations []time.Duration

	lastETACalc time.Time
	cachedETA   string
	finished    bool
}

type progressSample struct {
	timestamp time.Time
	bytes     int64
}

func NewProgressTracker(writer io.Writer, totalBytes int64, action string) *ProgressTracker {
	now := time.Now()
	return &ProgressTracker{
		Writer:        writer,
		TotalBytes:    totalBytes,
		Action:        action,
		startTime:     now,
		lastChunkTime: now,
	}
}

func (pt *ProgressTracker) Update(bytesSent int64) {
	pt.currentBytes = bytesSent
	pt.display()
}

func (pt *ProgressTracker) IncrementChunk() {
	now := time.Now()
	duration := now.Sub(pt.lastChunkTime)
	pt.chunkDurations = append(pt.chunkDurations, duration)
	// Keep last 20 chunks for moving average
	if len(pt.chunkDurations) > 20 {
		pt.chunkDurations = pt.chunkDurations[1:]
	}
	pt.lastChunkTime = now
	pt.currentChunks++
	pt.display()
}

func (pt *ProgressTracker) IncrementRetry() {
	pt.retries++
	pt.display()
}

func (pt *ProgressTracker) SetTotalChunks(total int) {
	pt.TotalChunks = total
}

func (pt *ProgressTracker) Finish() {
	pt.finished = true
	pt.display()
	if pt.Writer != nil {
		fmt.Fprint(pt.Writer, "\n")
	}
}

func (pt *ProgressTracker) display() {
	if pt.Writer == nil {
		return
	}

	now := time.Now()
	var speed float64

	// Update Speed (sliding window)
	isDownloading := strings.HasPrefix(pt.Action, "Download:")
	if isDownloading {
		if len(pt.samples) == 0 || now.Sub(pt.samples[len(pt.samples)-1].timestamp) > 100*time.Millisecond {
			pt.samples = append(pt.samples, progressSample{now, pt.currentBytes})
		}
		cutoff := now.Add(-5 * time.Second)
		for len(pt.samples) > 2 && pt.samples[1].timestamp.Before(cutoff) {
			pt.samples = pt.samples[1:]
		}
		if len(pt.samples) > 1 {
			first := pt.samples[0]
			last := pt.samples[len(pt.samples)-1]
			dt := last.timestamp.Sub(first.timestamp).Seconds()
			if dt > 0 {
				speed = float64(last.bytes-first.bytes) / dt
			}
		}
	} else {
		elapsed := now.Sub(pt.startTime)
		if elapsed > 0 {
			speed = float64(pt.currentBytes) / elapsed.Seconds()
		}
	}

	// Recalculate ETA at most once per second
	if now.Sub(pt.lastETACalc) >= 1*time.Second {
		eta := ""
		if isDownloading {
			if pt.TotalBytes > 0 && pt.currentBytes < pt.TotalBytes {
				if speed > 0 {
					remaining := pt.TotalBytes - pt.currentBytes
					d := time.Duration(float64(remaining)/speed) * time.Second
					eta = "ETA " + formatDuration(d)
				} else {
					eta = "ETA --"
				}
			}
		} else {
			if pt.TotalChunks > 0 && pt.currentChunks < pt.TotalChunks {
				if len(pt.chunkDurations) > 0 {
					var totalDur time.Duration
					for _, d := range pt.chunkDurations {
						totalDur += d
					}
					avgDur := totalDur / time.Duration(len(pt.chunkDurations))
					remainingChunks := pt.TotalChunks - pt.currentChunks
					d := avgDur * time.Duration(remainingChunks)
					eta = "ETA " + formatDuration(d)
				} else {
					eta = "ETA --"
				}
			}
		}
		pt.cachedETA = eta
		pt.lastETACalc = now
	}

	// Always clear ETA if finished
	isFinished := (pt.currentBytes >= pt.TotalBytes && pt.TotalBytes > 0) || pt.finished
	if isFinished {
		pt.cachedETA = ""
	}

	const barWidth = 24
	percent := float64(0)
	if pt.TotalBytes > 0 {
		percent = float64(pt.currentBytes) / float64(pt.TotalBytes)
	}
	if percent > 1 {
		percent = 1
	}

	// Build statistics
	stats := []string{fmt.Sprintf("%s/s", FormatBytes(int64(speed)))}
	stats = append(stats, fmt.Sprintf("%s/%s", FormatBytes(pt.currentBytes), FormatBytes(pt.TotalBytes)))

	if pt.TotalChunks > 0 {
		stats = append(stats, fmt.Sprintf("%d/%d chunks", pt.currentChunks, pt.TotalChunks))
	}

	if pt.cachedETA != "" {
		stats = append(stats, pt.cachedETA)
	}

	if pt.retries > 0 {
		stats = append(stats, fmt.Sprintf("%d retries", pt.retries))
	}

	// Determine icon and clean action text
	spinner := "⠋"
	symbol := ColorCyan + spinner
	cleanAction := pt.Action
	isUpload := strings.HasPrefix(pt.Action, "Upload:")
	isDownload := strings.HasPrefix(pt.Action, "Download:")

	if isUpload {
		symbol = ColorBlue + "↑"
		cleanAction = strings.TrimSpace(strings.TrimPrefix(pt.Action, "Upload:"))
	} else if isDownload {
		symbol = ColorBlue + "↓"
		cleanAction = strings.TrimSpace(strings.TrimPrefix(pt.Action, "Download:"))
	}

	if isFinished {
		// Clean line when finished
		finishSymbol := ColorGreen + "✔"
		if isUpload {
			finishSymbol = ColorBlue + "↑"
		} else if isDownload {
			finishSymbol = ColorGreen + "↓"
		}
		fmt.Fprintf(pt.Writer, "\r%s%s %s %s%s%s\033[K", finishSymbol, ColorReset, cleanAction, ColorDim, strings.Join(stats, " · "), ColorReset)
		return
	}

	completed := int(percent * barWidth)
	if completed < 0 {
		completed = 0
	}
	remaining := barWidth - completed

	// Use Unicode for the progress bar
	bar := fmt.Sprintf("%s%s%s%s",
		ColorBlue, strings.Repeat("━", completed),
		ColorDim, strings.Repeat("━", remaining),
	)

	fmt.Fprintf(pt.Writer, "\r%s %s %s %s%3.0f%%%s %s(%s)%s\033[K",
		symbol, cleanAction, bar, ColorBlue, percent*100, ColorReset,
		ColorDim, strings.Join(stats, " · "), ColorReset)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// ProgressReader wraps an io.Reader and reports progress via a tracker.
type ProgressReader struct {
	io.Reader
	Tracker *ProgressTracker
}

func NewProgressReader(r io.Reader, total int64, w io.Writer) *ProgressReader {
	return &ProgressReader{
		Reader:  r,
		Tracker: NewProgressTracker(w, total, "Download: "),
	}
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Tracker.currentBytes += int64(n)
	pr.Tracker.display()

	if err == io.EOF {
		pr.Tracker.Finish()
	}

	return n, err
}

// Confirm asks the user for confirmation (y/n) via the given reader and writer.
func Confirm(w io.Writer, r io.Reader, message string) (bool, error) {
	fmt.Fprintf(w, "%s [y/N]: ", message)
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil // EOF
	}
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if response == "y" || response == "yes" {
		return true, nil
	}
	return false, nil
}

// IsInteractiveTTY returns true when stderr is connected to a real terminal.
// Progress output goes to stderr, so that is the relevant fd to check.
func IsInteractiveTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// Spinner handles a simple Unicode terminal spinner.
type Spinner struct {
	Frames   []string
	Interval time.Duration

	mu    sync.Mutex
	index int
	stop  chan struct{}
	done  chan struct{}
}

// NewSpinner creates a new spinner with default frames and interval.
func NewSpinner() *Spinner {
	return &Spinner{
		Frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"},
		Interval: 100 * time.Millisecond,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start runs the spinner in a background goroutine.
func (s *Spinner) Start() {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				s.index++
				s.mu.Unlock()
			case <-s.stop:
				return
			}
		}
	}()
}

// Current returns the current spinner frame.
func (s *Spinner) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Frames[s.index%len(s.Frames)]
}

// Stop stops the spinner background goroutine.
func (s *Spinner) Stop() {
	close(s.stop)
	<-s.done
}
