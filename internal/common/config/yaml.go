package config

import (
	"time"
)

// Duration is a wrapper around time.Duration that implements yaml.Unmarshaler
type Duration time.Duration

func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// ToDuration converts the custom Duration type back to time.Duration
func (d *Duration) ToDuration() time.Duration {
	return time.Duration(*d)
}
