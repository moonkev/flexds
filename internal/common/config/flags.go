package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// Uint32SliceFlag implements flag.Value for a slice of uint32
type Uint32SliceFlag []uint32

func (f *Uint32SliceFlag) String() string {
	if f == nil {
		return ""
	}
	strs := make([]string, len(*f))
	for i, v := range *f {
		strs[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(strs, ",")
}

func (f *Uint32SliceFlag) Set(value string) error {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid port value %q: %w", part, err)
		}
		*f = append(*f, uint32(v))
	}
	return nil
}

// LogLevelFlag implements flag.Value for slog.Level
type LogLevelFlag slog.Level

func (f *LogLevelFlag) String() string {
	return slog.Level(*f).String()
}

func (f *LogLevelFlag) Set(value string) error {
	switch strings.ToLower(value) {
	case "debug":
		*f = LogLevelFlag(slog.LevelDebug)
	case "info":
		*f = LogLevelFlag(slog.LevelInfo)
	case "warn", "warning":
		*f = LogLevelFlag(slog.LevelWarn)
	case "error":
		*f = LogLevelFlag(slog.LevelError)
	default:
		return fmt.Errorf("invalid log level %q: must be debug, info, warn, or error", value)
	}
	return nil
}

func (f *LogLevelFlag) Level() slog.Level {
	return slog.Level(*f)
}
