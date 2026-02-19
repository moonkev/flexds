package config

import (
	"fmt"
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
