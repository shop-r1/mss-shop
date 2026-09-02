package mallsettings

import "os"

const (
	// WriteEnabledEnvironment is deliberately module-specific. The capability
	// is fail-closed: an absent, empty, differently cased or unknown value never
	// enables a legacy metadata write.
	WriteEnabledEnvironment = "R1SHOP_MALL_SETTINGS_WRITE_ENABLED"
	writeEnabledValue       = "true"
)

type writeCapability struct {
	enabled bool
}

func environmentWriteCapability() writeCapability {
	return writeCapabilityForValue(os.Getenv(WriteEnabledEnvironment))
}

func writeCapabilityForValue(value string) writeCapability {
	return writeCapability{enabled: value == writeEnabledValue}
}

func (capability writeCapability) allowsUpdate() bool {
	return capability.enabled
}
