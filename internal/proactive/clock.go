package proactive

import "time"

type timer interface {
	Stop() bool
}

type clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) timer
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) timer { return time.AfterFunc(d, f) }
