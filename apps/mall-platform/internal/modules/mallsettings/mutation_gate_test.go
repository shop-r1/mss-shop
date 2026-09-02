package mallsettings

import "testing"

func TestWriteCapabilityIsFailClosed(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "1", "TRUE", " true", "true ", "enabled", "isolated-cutover"} {
		if writeCapabilityForValue(value).allowsUpdate() {
			t.Errorf("write capability unexpectedly enabled for %q", value)
		}
	}
	if !writeCapabilityForValue("true").allowsUpdate() {
		t.Fatal("exact documented write value did not enable the capability")
	}
}
