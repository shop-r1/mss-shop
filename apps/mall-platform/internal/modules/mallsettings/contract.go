// Package mallsettings restores the reviewed, non-sensitive subset of the
// legacy mall configuration workflow inside the MSS business-module boundary.
package mallsettings

const (
	moduleName = "mallsettings"

	PermissionRead   = "mall-settings:read"
	PermissionUpdate = "mall-settings:update"

	generalRoutePath = "/admin/api/mall-settings/general"
	menuPath         = "/business/settings/mall-settings"
	readComponent    = menuPath + "/permissions/read"
	updateComponent  = menuPath + "/permissions/update"

	legacyConfigName = "appConfig"

	// postgresPrivateRelation is the only PostgreSQL relation allowed to expose
	// the raw appConfig document to this dedicated workflow. The generic
	// system_configs compatibility view remains metadata-redacted and read-only.
	postgresPrivateRelation = "r1_mall_settings_system_configs"
)

// legacyGeneralField is the complete reviewed mapping between the public DTO
// and the legacy appConfig metadata object. Keeping it explicit prevents the
// HTTP boundary from becoming an arbitrary metadata editor.
type legacyGeneralField struct {
	APIName     string
	MetadataKey string
}

var legacyGeneralFields = struct {
	MallName           legacyGeneralField
	OrderPrefix        legacyGeneralField
	DefaultSenderName  legacyGeneralField
	DefaultSenderPhone legacyGeneralField
}{
	MallName:           legacyGeneralField{APIName: "mallName", MetadataKey: "mall_name"},
	OrderPrefix:        legacyGeneralField{APIName: "orderPrefix", MetadataKey: "ewePrefix"},
	DefaultSenderName:  legacyGeneralField{APIName: "defaultSenderName", MetadataKey: "default_sender_name"},
	DefaultSenderPhone: legacyGeneralField{APIName: "defaultSenderPhone", MetadataKey: "default_sender_phone"},
}
