package systemd

import _ "embed"

//go:embed host-telemetry.service
var Template string
