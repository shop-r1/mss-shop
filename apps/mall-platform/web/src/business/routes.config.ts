import type { AdminBusinessRoute } from '@mss-boot-io/admin-web/business';
import { LEGACY_RESOURCES } from './legacy/catalog';
import { mallGeneralSettingsPermissionPaths } from './mall-settings/paths';

// Add handwritten Umi business routes here. Generated AdminModule routes are
// composed ahead of this array by the managed config/business-routes.ts facade.
const businessRoutes: AdminBusinessRoute[] = [
  {
    path: mallGeneralSettingsPermissionPaths.route,
    component: '@/business/mall-settings',
    name: 'mallSettings',
  },
  ...LEGACY_RESOURCES.map((entry) => ({
    path: entry.path,
    component: '@/business/legacy/ResourcePage',
    name: entry.menuName,
  })),
];

export default businessRoutes;
