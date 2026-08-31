import type { RouteRegistration } from '@mss-boot-io/admin-web/runtime';
import { SHARED_CATALOG_RESOURCES } from './shared-catalog/catalog';

// Project handwritten server paths into frontend menu and route visibility here.
// This does not create Admin Menu/Casbin rows or authorize backend requests.
const routeRegistrations: readonly RouteRegistration[] = SHARED_CATALOG_RESOURCES.map((entry) => ({
  path: entry.path,
  serverPaths: [entry.path],
  // The authorized-menu API normalizes stored names before ProLayout sees them.
  menuName: entry.resource,
  permission: entry.routePermission,
}));

export default routeRegistrations;
