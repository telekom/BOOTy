package timesync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// CodeNoTime is the failure code when no time source answers and
// TIME_SYNC=require.
const CodeNoTime = "BOOTY-TIM-001"

// ErrNoTime is returned by Sync when TIME_SYNC=require and no source gave a
// usable time.
var ErrNoTime = errors.New(CodeNoTime + ": no time source gave a usable time")

// minPlausibleTime is the earliest time that BOOTy accepts as the current
// time when the binary carries no VCS commit time. A clock before it is
// certainly wrong.
var minPlausibleTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// Options select the time sources of one Sync.
type Options struct {
	// Require fails Sync when no source answers (TIME_SYNC=require).
	Require bool
	// NTPServers are queried in order; the first good answer counts.
	NTPServers []string
	// DateURL is the CAPRF URL whose Date header is the baseline and the
	// sanity bound for NTP. Empty skips it.
	DateURL string
	// MaxSkew is the largest difference between NTP and the authenticated
	// Date header time that BOOTy accepts (TIME_MAX_SKEW_SEC).
	MaxSkew time.Duration
	// WriteRTC writes the RTC after the clock was stepped (TIME_WRITE_RTC).
	WriteRTC bool
}

// Result describes a finished Sync.
type Result struct {
	// Source is the source that set the clock, empty when none answered.
	Source string
	// Offset is the measured skew of the clock before the sync: the source
	// time minus the local clock.
	Offset time.Duration
	// Stepped is true when the clock was set.
	Stepped bool
	// RTCWritten is true when the RTC was written.
	RTCWritten bool
}

// Syncer runs a time sync. The zero value is not usable; see NewSyncer.
type Syncer struct {
	Now      func() time.Time
	Floor    time.Time
	NTP      func(ctx context.Context, server string) (Sample, error)
	Date     func(ctx context.Context, rawURL string) (Sample, error)
	SetClock func(time.Time) error
	WriteRTC func(time.Time) error
	Log      *slog.Logger
}

// Sync measures the clock against the configured sources and steps it.
// Order of preference: NTP, checked against the authenticated CAPRF Date
// time when both answer, then the Date time alone, then the RTC as it is.
// Without Require, every failure is only logged.
func (s Syncer) Sync(ctx context.Context, opts Options) (Result, error) {
	ntp, ntpOK := s.queryNTP(ctx, opts.NTPServers)
	date, dateOK := s.probeDate(ctx, opts.DateURL)
	chosen, ok := s.choose(ntp, ntpOK, date, dateOK, opts.MaxSkew)
	if !ok {
		return s.noSource(opts.Require)
	}
	res := Result{Source: chosen.Source, Offset: chosen.Offset}
	s.Log.Info("measured clock skew", "source", chosen.Source, "skew", chosen.Offset.Round(time.Millisecond),
		"skew_seconds", chosen.Offset.Seconds(), "authenticated", chosen.Authenticated)
	if chosen.Offset.Abs() < chosen.Resolution {
		last.Store(&res)
		return res, nil
	}
	target := s.Now().Add(chosen.Offset)
	if err := s.SetClock(target); err != nil {
		err = fmt.Errorf("set the system clock: %w", err)
		if opts.Require {
			return res, err
		}
		s.Log.Warn("setting the system clock failed", "error", err)
		return res, nil
	}
	res.Stepped = true
	s.Log.Info("clock synchronized", "source", chosen.Source, "time", target.UTC().Format(time.RFC3339), "corrected_by", chosen.Offset.Round(time.Millisecond))
	if opts.WriteRTC {
		if err := s.WriteRTC(s.Now()); err != nil {
			s.Log.Warn("writing the RTC failed, the installed OS starts with the old time", "error", err)
		} else {
			res.RTCWritten = true
			s.Log.Info("RTC updated")
		}
	}
	last.Store(&res)
	return res, nil
}

func (s Syncer) queryNTP(ctx context.Context, servers []string) (Sample, bool) {
	for _, server := range servers {
		sample, err := s.NTP(ctx, server)
		if err != nil {
			s.Log.Warn("NTP query failed", "server", server, "error", err)
			continue
		}
		if s.beforeFloor(sample) {
			continue
		}
		return sample, true
	}
	return Sample{}, false
}

func (s Syncer) probeDate(ctx context.Context, rawURL string) (Sample, bool) {
	if rawURL == "" {
		return Sample{}, false
	}
	sample, err := s.Date(ctx, rawURL)
	if err != nil {
		s.Log.Warn("reading the time from the CAPRF Date header failed", "error", err)
		return Sample{}, false
	}
	return sample, !s.beforeFloor(sample)
}

// beforeFloor rejects a source time before the build of this binary.
func (s Syncer) beforeFloor(sample Sample) bool {
	t := s.Now().Add(sample.Offset)
	if t.Before(s.Floor) {
		s.Log.Warn("ignoring a time source that is before the build of BOOTy",
			"source", sample.Source, "time", t.UTC().Format(time.RFC3339), "floor", s.Floor.UTC().Format(time.RFC3339))
		return true
	}
	return false
}

// choose prefers NTP for its precision. An authenticated Date time bounds
// it: an NTP answer that differs more than maxSkew is not used, since plain
// NTP is not authenticated.
func (s Syncer) choose(ntp Sample, ntpOK bool, date Sample, dateOK bool, maxSkew time.Duration) (Sample, bool) {
	switch {
	case ntpOK && dateOK && date.Authenticated:
		if diff := (ntp.Offset - date.Offset).Abs(); diff > maxSkew {
			s.Log.Error("NTP time differs from the authenticated CAPRF Date time, using the Date time",
				"ntp", ntp.Source, "difference", diff.Round(time.Second), "max_skew", maxSkew)
			return date, true
		}
		return ntp, true
	case ntpOK:
		return ntp, true
	case dateOK:
		return date, true
	default:
		return Sample{}, false
	}
}

func (s Syncer) noSource(require bool) (Result, error) {
	now := s.Now()
	suspect := now.Before(s.Floor)
	if require {
		if suspect {
			return Result{}, fmt.Errorf("%w; the clock %s is before the build of BOOTy", ErrNoTime, now.UTC().Format(time.RFC3339))
		}
		return Result{}, ErrNoTime
	}
	if suspect {
		s.Log.Error("no time source answered and the clock is before the build of BOOTy; TLS certificate checks will fail",
			"clock", now.UTC().Format(time.RFC3339), "code", CodeNoTime)
	} else {
		s.Log.Warn("no time source answered, keeping the RTC time", "clock", now.UTC().Format(time.RFC3339))
	}
	return Result{}, nil
}

// BuildFloor returns the earliest plausible current time: the VCS commit
// time of the binary, or minPlausibleTime when that is earlier or unknown.
func BuildFloor() time.Time {
	floor := minPlausibleTime
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return floor
	}
	for _, setting := range info.Settings {
		if setting.Key != "vcs.time" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, setting.Value); err == nil && t.After(floor) {
			floor = t
		}
	}
	return floor
}
