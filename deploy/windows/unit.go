package winunit

import (
	"strings"
	"time"
)

const (
	Name        = "host-telemetry"
	DisplayName = "host-telemetry"
	Description = "Host telemetry collector: one schema for every OS."
	StartType   = "auto"
)

type FailureAction struct {
	Restart bool
	Delay   time.Duration
}

type Spec struct {
	Name           string
	DisplayName    string
	Description    string
	Executable     string
	Arguments      []string
	StartType      string
	Dependencies   []string
	FailureReset   time.Duration
	FailureActions []FailureAction
}

func DefaultSpec(executable, configPath string) Spec {
	return Spec{
		Name:         Name,
		DisplayName:  DisplayName,
		Description:  Description,
		Executable:   executable,
		Arguments:    []string{"run", "--config", configPath},
		StartType:    StartType,
		Dependencies: []string{"Tcpip"},
		FailureReset: 24 * time.Hour,
		FailureActions: []FailureAction{
			{Restart: true, Delay: 5 * time.Second},
			{Restart: true, Delay: 15 * time.Second},
			{Restart: true, Delay: 60 * time.Second},
		},
	}
}

func CommandLine(s Spec) string {
	parts := make([]string, 0, len(s.Arguments)+1)
	parts = append(parts, Quote(s.Executable))
	for _, arg := range s.Arguments {
		parts = append(parts, Quote(arg))
	}
	return strings.Join(parts, " ")
}

func Quote(s string) string {
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
