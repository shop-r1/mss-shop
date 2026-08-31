import type { RouteRegistration } from '@mss-boot-io/admin-web/runtime';
import { LEGACY_RESOURCES } from './legacy/catalog';

// Project handwritten server paths into frontend menu and route visibility here.
// This does not create Admin Menu/Casbin rows or authorize backend requests.
const routeRegistrations: readonly RouteRegistration[] = LEGACY_RESOURCES.map((entry) => ({
  path: entry.path,
  serverPaths: [entry.path],
  // The authorized-menu API normalizes every stored name to its last segment.
  // A relative leaf keeps ProLayout's hierarchical locale ID deterministic.
  menuName: entry.resource,
  permission: entry.routePermission,
}));

export default routeRegistrations;
