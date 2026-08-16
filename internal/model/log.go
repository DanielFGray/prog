package model

import "time"

// CollapsedLog is a log entry prepared for display. Runs of consecutive
// entries with the same message are merged into one entry carrying a count.
type CollapsedLog struct {
	Message   string
	CreatedAt time.Time // Time of the first entry in the run
	Count     int
}

// CollapseLogs merges runs of consecutive entries with identical messages into
// a single entry with a count. It expects logs ordered by time, as GetLogs
// returns them. A message repeated non-consecutively stays in separate entries,
// so interleaved progress is not hidden. This is a derived view for display;
// stored history is untouched.
func CollapseLogs(logs []Log) []CollapsedLog {
	var collapsed []CollapsedLog
	for _, log := range logs {
		if n := len(collapsed); n > 0 && collapsed[n-1].Message == log.Message {
			collapsed[n-1].Count++
			continue
		}
		collapsed = append(collapsed, CollapsedLog{
			Message:   log.Message,
			CreatedAt: log.CreatedAt,
			Count:     1,
		})
	}
	return collapsed
}
