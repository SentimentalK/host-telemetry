package darwin_test

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
)

func TestSensorsNullSkipped(t *testing.T) {
	samples := realSynthetic(t)
	path := "real-synthetic-01/macmon.json"
	data := samples[path].Data
	null := bytes.Replace(data, []byte(`"gpu_power": 2.87`), []byte(`"gpu_power": null`), 1)
	if bytes.Equal(null, data) {
		t.Fatal("macmon fixture has no gpu_power value to null")
	}
	samples[path] = &fstest.MapFile{Data: null}

	points, err := collect(t, darwin.New(samples), "darwin-sensors")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pointWithAttr(points, schema.HWPower, schema.AttrHWID, "gpu"); ok {
		t.Errorf("%s for gpu emitted from null", schema.HWPower)
	}
	if _, ok := pointWithAttr(points, schema.HWTemperature, schema.AttrHWID, "cpu"); !ok {
		t.Errorf("%s for cpu missing", schema.HWTemperature)
	}
}

func TestUptimeRejectsNowBeforeBoot(t *testing.T) {
	samples := realSynthetic(t)
	samples["real-synthetic-01/now.txt"] = &fstest.MapFile{Data: []byte("1763399999\n")}

	if _, err := collect(t, darwin.New(samples), "darwin-uptime"); err == nil {
		t.Fatal("darwin-uptime accepted now before boot")
	}
}

func TestInventoryRejectsMissingMemsize(t *testing.T) {
	samples := realSynthetic(t)
	path := "real-synthetic-01/sysctl.txt"
	data := samples[path].Data
	missing := bytes.Replace(data, []byte("hw.memsize: 17179869184\n"), nil, 1)
	if bytes.Equal(missing, data) {
		t.Fatal("sysctl fixture has no hw.memsize to remove")
	}
	samples[path] = &fstest.MapFile{Data: missing}

	if _, err := collect(t, darwin.New(samples), "darwin-inventory"); err == nil {
		t.Fatal("darwin-inventory accepted sysctl without hw.memsize")
	}
}
