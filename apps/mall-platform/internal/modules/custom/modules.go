// Package custom registers handwritten business modules that are not generated
// from an AdminModule specification.
package custom

import (
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/modules/legacycompat"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/modules/mallsettings"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/modules/memberlevels"
)

// Modules returns a fresh slice of explicitly registered custom modules. Each
// module owns its forward authorization migration, readiness, and handler checks.
func Modules() []business.Module {
	return []business.Module{legacycompat.Module(), mallsettings.Module(), memberlevels.Module()}
}
